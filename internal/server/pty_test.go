package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

// fakeTerminal stands in for a held daemon attach. out is what the session
// prints; closing it ends the stream with endErr (io.EOF unless set).
type fakeTerminal struct {
	out      chan []byte
	endErr   error
	writable bool

	written chan []byte
	resized chan [2]int

	closeOnce sync.Once
	closed    chan struct{}
}

func newFakeTerminal(writable bool) *fakeTerminal {
	return &fakeTerminal{
		out:      make(chan []byte, 16),
		writable: writable,
		written:  make(chan []byte, 16),
		resized:  make(chan [2]int, 16),
		closed:   make(chan struct{}),
	}
}

func (f *fakeTerminal) Read(p []byte) (int, error) {
	select {
	case b, ok := <-f.out:
		if !ok {
			if f.endErr != nil {
				return 0, f.endErr
			}
			return 0, io.EOF
		}
		return copy(p, b), nil
	case <-f.closed:
		return 0, net.ErrClosed
	}
}

func (f *fakeTerminal) Write(p []byte) (int, error) {
	f.written <- append([]byte(nil), p...)
	return len(p), nil
}

func (f *fakeTerminal) Resize(_ context.Context, cols, rows int) error {
	f.resized <- [2]int{cols, rows}
	return nil
}

func (f *fakeTerminal) Writable() bool { return f.writable }

func (f *fakeTerminal) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

type attachCall struct {
	session    string
	cols, rows int
}

// ptyDeps wires one fake terminal (or one attach error) into the router and
// records every attach the bridge asks for.
func ptyDeps(term *fakeTerminal, attachErr error) (Deps, <-chan attachCall) {
	calls := make(chan attachCall, 4)
	return Deps{
		Attach: func(_ context.Context, session string, cols, rows int) (Terminal, error) {
			calls <- attachCall{session, cols, rows}
			if attachErr != nil {
				return nil, attachErr
			}
			return term, nil
		},
	}, calls
}

func dialPTY(t *testing.T, url string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if conn != nil {
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

func readControl(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read control message: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("want a text control message, got %v %q", typ, data)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("control message is not JSON: %q", data)
	}
	return msg
}

// closeStatus reads until the server closes the socket and returns its code and reason.
func closeStatus(t *testing.T, conn *websocket.Conn) (websocket.StatusCode, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code, ce.Reason
		}
		t.Fatalf("socket ended without a close frame: %v", err)
	}
}

func noAttach(t *testing.T, calls <-chan attachCall) {
	t.Helper()
	select {
	case c := <-calls:
		t.Errorf("the daemon was attached to (%+v) for a request that should have been refused first", c)
	default:
	}
}

// The first line of defence for a socket that types into a live session: a page
// on any other site can open a WebSocket to the operator's loopback, and the
// browser sends no preflight for it, so the origin is the whole check here.
func TestPTYRefusesAForeignOriginBeforeAttaching(t *testing.T) {
	d, calls := ptyDeps(newFakeTerminal(true), nil)
	url, _ := wsServer(t, d)
	_, resp, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", http.Header{"Origin": {"https://evil.example"}})
	if err == nil {
		t.Fatal("a foreign origin opened a terminal socket")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %v, want 403", resp)
	}
	noAttach(t, calls)
}

// guard already refuses a foreign origin before any handler runs, so the test above
// cannot tell whether the handler checks on its own. It must: terminalAllowed is
// where a second line of defence for this socket goes, and a route mounted without
// the middleware must not become a terminal any page can open.
func TestPTYHandlerRefusesAForeignOriginWithoutTheMiddleware(t *testing.T) {
	d, calls := ptyDeps(newFakeTerminal(true), nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/{id}/pty", d.handlePTY)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	_, resp, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", http.Header{"Origin": {"https://evil.example"}})
	if err == nil {
		t.Fatal("the handler opened a terminal for a foreign origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %v, want 403", resp)
	}
	noAttach(t, calls)
}

func TestPTYRefusesMissingOrOutOfRangeGeometryBeforeAttaching(t *testing.T) {
	for _, q := range []string{
		"", "?cols=80", "?rows=24", "?cols=0&rows=24", "?cols=80&rows=0",
		"?cols=-1&rows=24", "?cols=abc&rows=24", "?cols=100000&rows=24", "?cols=80&rows=100000",
	} {
		d, calls := ptyDeps(newFakeTerminal(true), nil)
		url, _ := wsServer(t, d)
		_, resp, err := dialPTY(t, url+"/api/sessions/abc/pty"+q, nil)
		if err == nil {
			t.Errorf("%q: opened a terminal with an invalid geometry", q)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status = %v, want 400", q, resp)
		}
		noAttach(t, calls)
	}
}

func TestPTYAttachesToThePathSessionAtTheAskedGeometry(t *testing.T) {
	d, calls := ptyDeps(newFakeTerminal(true), nil)
	url, _ := wsServer(t, d)
	if _, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=132&rows=41", nil); err != nil {
		t.Fatalf("dial: %v", err)
	}
	select {
	case c := <-calls:
		if c != (attachCall{"abc", 132, 41}) {
			t.Errorf("attach = %+v, want abc at 132x41", c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the bridge never attached")
	}
}

func TestPTYTellsTheBrowserWhetherItCanType(t *testing.T) {
	for _, writable := range []bool{true, false} {
		d, _ := ptyDeps(newFakeTerminal(writable), nil)
		url, _ := wsServer(t, d)
		conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		msg := readControl(t, conn)
		if msg["type"] != "ready" || msg["writable"] != writable {
			t.Errorf("first message = %v, want ready with writable=%v", msg, writable)
		}
	}
}

func TestPTYStreamsSessionOutputAsBinaryFrames(t *testing.T) {
	term := newFakeTerminal(true)
	d, _ := ptyDeps(term, nil)
	url, _ := wsServer(t, d)
	conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readControl(t, conn)
	term.out <- []byte("\x1b[1mhello")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageBinary || string(data) != "\x1b[1mhello" {
		t.Errorf("got %v %q, want the bytes as one binary frame", typ, data)
	}
}

func TestPTYTypesBinaryFramesIntoTheSession(t *testing.T) {
	term := newFakeTerminal(true)
	d, _ := ptyDeps(term, nil)
	url, _ := wsServer(t, d)
	conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readControl(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("ls\r")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case got := <-term.written:
		if string(got) != "ls\r" {
			t.Errorf("session received %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing reached the session")
	}
}

// Without a control key the terminal still shows the session, and typing into
// it is refused where the operator can see it — never swallowed.
func TestPTYReadOnlyTerminalRefusesInputVisibly(t *testing.T) {
	term := newFakeTerminal(false)
	d, _ := ptyDeps(term, nil)
	url, _ := wsServer(t, d)
	conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readControl(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	msg := readControl(t, conn)
	if msg["type"] != "error" || msg["error"] == "" {
		t.Errorf("input to a read-only terminal answered %v, want an error message", msg)
	}
	select {
	case got := <-term.written:
		t.Errorf("%q reached a session through a terminal that cannot type", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPTYResizeMessageReachesTheTerminal(t *testing.T) {
	term := newFakeTerminal(true)
	d, _ := ptyDeps(term, nil)
	url, _ := wsServer(t, d)
	conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readControl(t, conn)
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case got := <-term.resized:
		if got != [2]int{100, 30} {
			t.Errorf("resize = %v, want 100x30", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the resize never reached the terminal")
	}
}

// A geometry the bridge would refuse at connect is refused mid-stream too, and
// is never passed on: a resize reaches a PTY that other people are watching.
func TestPTYRejectsMalformedControlMessages(t *testing.T) {
	for _, msg := range []string{
		`{"type":"resize","cols":0,"rows":30}`,
		`{"type":"resize","cols":100000,"rows":30}`,
		`{"type":"resize","cols":100}`,
		`{"type":"launch"}`,
		// A well-formed geometry under any other type must not become a resize: the
		// type is what says what the numbers are for.
		`{"type":"launch","cols":100,"rows":30}`,
		`{"cols":100,"rows":30}`,
		`not json`,
	} {
		term := newFakeTerminal(true)
		d, _ := ptyDeps(term, nil)
		url, _ := wsServer(t, d)
		conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		readControl(t, conn)
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(msg)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if code, _ := closeStatus(t, conn); code != statusBadControl {
			t.Errorf("%s: closed with %d, want %d", msg, code, statusBadControl)
		}
		select {
		case got := <-term.resized:
			t.Errorf("%s: forwarded as resize %v", msg, got)
		default:
		}
	}
}

func TestPTYKickClosesWithItsOwnCodeAndTheReason(t *testing.T) {
	term := newFakeTerminal(true)
	term.endErr = &daemon.ErrKicked{Detail: "Session opened in another window"}
	d, _ := ptyDeps(term, nil)
	url, _ := wsServer(t, d)
	conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readControl(t, conn)
	close(term.out)
	code, reason := closeStatus(t, conn)
	if code != statusKicked || !strings.Contains(reason, "Session opened in another window") {
		t.Errorf("closed with %d %q, want %d carrying the daemon's reason", code, reason, statusKicked)
	}
}

func TestPTYSessionEndClosesWithItsOwnCode(t *testing.T) {
	term := newFakeTerminal(true)
	d, _ := ptyDeps(term, nil)
	url, _ := wsServer(t, d)
	conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readControl(t, conn)
	close(term.out)
	if code, _ := closeStatus(t, conn); code != statusSessionEnded {
		t.Errorf("closed with %d, want %d", code, statusSessionEnded)
	}
}

func TestPTYAttachFailuresCloseWithCodesTheBrowserCanTellApart(t *testing.T) {
	cases := []struct {
		err  error
		want websocket.StatusCode
	}{
		{&daemon.ErrNojob{}, statusNoSuchSession},
		{&daemon.ErrAuth{}, statusKeyRefused},
		{daemon.ErrDaemonUnavailable, statusDaemonUnavailable},
		{errors.New("something else"), websocket.StatusInternalError},
	}
	for _, c := range cases {
		d, _ := ptyDeps(nil, c.err)
		url, _ := wsServer(t, d)
		conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
		if err != nil {
			t.Fatalf("%v: dial: %v", c.err, err)
		}
		if code, _ := closeStatus(t, conn); code != c.want {
			t.Errorf("%v: closed with %d, want %d", c.err, code, c.want)
		}
	}
}

// A tab that closes must not leave an attach behind on the daemon: an attacher
// that lingers keeps its geometry on someone else's session.
func TestPTYBrowserLeavingClosesTheAttach(t *testing.T) {
	term := newFakeTerminal(true)
	d, _ := ptyDeps(term, nil)
	url, returned := wsServer(t, d)
	conn, _, err := dialPTY(t, url+"/api/sessions/abc/pty?cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readControl(t, conn)
	_ = conn.Close(websocket.StatusNormalClosure, "tab closed")
	select {
	case <-term.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the attach stayed open after the browser left")
	}
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler did not return after the browser left")
	}
}
