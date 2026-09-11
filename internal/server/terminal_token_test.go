package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The panel's own page reads the token. No Origin header is what a same-origin
// GET from the panel sends; the loopback origin is what the desktop window and a
// browser tab on the panel send when they do send one.
func TestTerminalTokenIsServedToThePanel(t *testing.T) {
	d, _ := ptyDeps(newFakeTerminal(true), nil)
	for _, origin := range []string{"", "http://127.0.0.1:7777"} {
		req := httptest.NewRequest(http.MethodGet, "/api/terminal-token", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		New(d).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("origin %q: status %d, want 200", origin, rec.Code)
		}
		var body struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Token != testToken {
			t.Errorf("origin %q: body %q, want the token", origin, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("origin %q: Cache-Control %q, want no-store: a token must not outlive the answer it came in", origin, rec.Header().Get("Cache-Control"))
		}
	}
}

// What keeps a page on another origin from reading the token is the browser: it
// hides the body of a cross-origin response unless the server opts in with CORS
// headers. So the server must never opt in — not on the GET, and not on a
// preflight. A page served by any other local server (another port on 127.0.0.1)
// passes the Origin rule, which checks the host and not the port; for that page
// this is the only thing standing between it and the token.
func TestTerminalTokenIsNeverSharedAcrossOrigins(t *testing.T) {
	d, _ := ptyDeps(newFakeTerminal(true), nil)
	for _, origin := range []string{"http://127.0.0.1:9999", "http://localhost:3000", "https://evil.example"} {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			req := httptest.NewRequest(method, "/api/terminal-token", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Access-Control-Request-Method", http.MethodGet)
			rec := httptest.NewRecorder()
			New(d).ServeHTTP(rec, req)
			for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Headers", "Access-Control-Allow-Methods"} {
				if v := rec.Header().Get(h); v != "" {
					t.Errorf("%s %s from %s: %s = %q; any CORS opt-in lets that page read the token", method, "/api/terminal-token", origin, h, v)
				}
			}
			if origin == "https://evil.example" && strings.Contains(rec.Body.String(), testToken) {
				t.Errorf("%s from %s: the token is in the body", method, origin)
			}
		}
	}
}

func TestTerminalTokenRouteWithoutATokenIsUnavailable(t *testing.T) {
	d, _ := ptyDeps(newFakeTerminal(true), nil)
	d.TerminalToken = ""
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/terminal-token", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

// A panel wired without a token has no second line of defence for a socket that
// types into sessions, and must not open one on the first line alone.
func TestPTYWithoutAConfiguredTokenOpensNothing(t *testing.T) {
	d, calls := ptyDeps(newFakeTerminal(true), nil)
	d.TerminalToken = ""
	url, _ := wsServer(t, d)
	_, resp, err := dialPTYRaw(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err == nil {
		t.Fatal("a terminal socket opened on a panel with no token")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %v, want 503", resp)
	}
	noAttach(t, calls)
}

// The second line of defence, tested on its own. The handler is mounted without
// guard and the request carries an origin the Origin rule accepts — a page on
// another local port — so nothing but the token check stands in the way here.
// Take the token check out and this test is the one that fails; the origin tests
// do not notice.
func TestPTYRefusesASocketThatDoesNotProveTheToken(t *testing.T) {
	cases := []struct {
		name  string
		first func(t *testing.T, conn *websocket.Conn)
	}{
		{"wrong token", func(t *testing.T, c *websocket.Conn) { sendAuth(t, c, "f"+testToken[1:]) }},
		{"empty token", func(t *testing.T, c *websocket.Conn) { sendAuth(t, c, "") }},
		{"the token and more", func(t *testing.T, c *websocket.Conn) { sendAuth(t, c, testToken+"0") }},
		{"a prefix of the token", func(t *testing.T, c *websocket.Conn) { sendAuth(t, c, testToken[:32]) }},
		// Binary frames are keystrokes on this socket; one that happens to spell the
		// auth message is still keystrokes, not a control message.
		{"keystrokes first", func(_ *testing.T, c *websocket.Conn) {
			_ = c.Write(context.Background(), websocket.MessageBinary, []byte(`{"type":"auth","token":"`+testToken+`"}`))
		}},
		{"another control message first", func(_ *testing.T, c *websocket.Conn) {
			_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"resize","cols":80,"rows":24,"token":"`+testToken+`"}`))
		}},
		{"not JSON", func(_ *testing.T, c *websocket.Conn) {
			_ = c.Write(context.Background(), websocket.MessageText, []byte("auth "+testToken))
		}},
		{"silence", func(*testing.T, *websocket.Conn) {}},
	}
	for _, c := range cases {
		d, calls := ptyDeps(newFakeTerminal(true), nil)
		d.terminalAuthTimeout = 200 * time.Millisecond
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/sessions/{id}/pty", d.handlePTY)
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		url := "ws" + strings.TrimPrefix(srv.URL, "http")

		conn, _, err := dialPTYRaw(t, url+"/api/sessions/abc/pty?cols=80&rows=24", http.Header{"Origin": {"http://127.0.0.1:9999"}})
		if err != nil {
			t.Fatalf("%s: dial: %v", c.name, err)
		}
		c.first(t, conn)
		code, reason := closeStatus(t, conn)
		if code != statusTokenRefused {
			t.Errorf("%s: closed with %d %q, want %d", c.name, code, reason, statusTokenRefused)
		}
		if reason == "" {
			t.Errorf("%s: refused without a reason the panel can show", c.name)
		}
		if strings.Contains(reason, testToken[:16]) {
			t.Errorf("%s: the close reason carries token material: %q", c.name, reason)
		}
		noAttach(t, calls)
	}
}

// Attaching resizes somebody's session, so it waits for the token rather than
// running alongside the check.
func TestPTYAttachesOnlyOnceTheTokenArrives(t *testing.T) {
	d, calls := ptyDeps(newFakeTerminal(true), nil)
	url, _ := wsServer(t, d)
	conn, _, err := dialPTYRaw(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	select {
	case c := <-calls:
		t.Fatalf("attached (%+v) before the token arrived", c)
	case <-time.After(150 * time.Millisecond):
	}
	sendAuth(t, conn, testToken)
	select {
	case <-calls:
	case <-time.After(2 * time.Second):
		t.Fatal("never attached after the token arrived")
	}
	if msg := readControl(t, conn); msg["type"] != "ready" {
		t.Errorf("first message after the token = %v, want ready", msg)
	}
}

// The snapshot is what every page and every push carries; the token must never
// ride along in it.
func TestTerminalTokenIsNotInTheSnapshot(t *testing.T) {
	d := wsDeps()
	d.TerminalToken = testToken
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/snapshot", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testToken) {
		t.Error("the terminal token is in /api/snapshot")
	}
}
