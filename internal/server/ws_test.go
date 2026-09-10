package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/state"
)

// wsServer starts the router on a loopback listener and returns its ws:// URL
// plus a channel closed when the handler returns. The wrapper around New is what
// makes "the handler exited" observable at all: without it a leaked push loop
// looks exactly like a healthy one from the client's side.
func wsServer(t *testing.T, d Deps) (string, <-chan struct{}) {
	t.Helper()
	handler := New(d)
	returned := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(returned)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), returned
}

func wsDeps() Deps {
	return Deps{
		Snapshot: func() state.Snapshot {
			return state.Snapshot{Cards: []board.Card{{Path: "/b/c.md", Stage: "active"}}}
		},
	}
}

func readSnapshot(t *testing.T, ctx context.Context, conn *websocket.Conn) state.Snapshot {
	t.Helper()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read from socket: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("want a text message, got %v", typ)
	}
	var snap state.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("push is not a snapshot: %v (%s)", err, data)
	}
	return snap
}

// The browser must not stare at an empty panel until the first tick, so the
// first snapshot goes out on connect. A ten-second cadence here means a snapshot
// arriving at all can only have come from that immediate push.
func TestWebSocketPushesASnapshotOnConnect(t *testing.T) {
	d := wsDeps()
	d.interval = 10 * time.Second
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	snap := readSnapshot(t, ctx, conn)
	if len(snap.Cards) != 1 || snap.Cards[0].Path != "/b/c.md" {
		t.Fatalf("the push did not carry the snapshot: %+v", snap)
	}
}

func TestWebSocketKeepsPushingOnItsCadence(t *testing.T) {
	d := wsDeps()
	d.interval = 20 * time.Millisecond
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	for i := range 3 {
		if snap := readSnapshot(t, ctx, conn); len(snap.Cards) != 1 {
			t.Fatalf("push %d did not carry the snapshot: %+v", i, snap)
		}
	}
}

// The server listens on the loopback interface only, so the only origins that
// can legitimately open this socket are the panel's own. Without this check any
// page the operator happens to visit could open the socket and read the whole
// fleet's state out of their browser.
func TestWebSocketRefusesAForeignOrigin(t *testing.T) {
	url, _ := wsServer(t, wsDeps())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example.com"}},
	})
	if err == nil {
		conn.CloseNow()
		t.Fatal("a foreign origin must not be able to open the socket")
	}
	if resp == nil {
		t.Fatalf("no handshake response to inspect, error was: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a foreign origin must be refused with 403, got %d", resp.StatusCode)
	}
}

func TestWebSocketAcceptsTheLocalOrigin(t *testing.T) {
	d := wsDeps()
	d.interval = 10 * time.Second
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://localhost:7777"}},
	})
	if err != nil {
		t.Fatalf("the panel's own origin must be accepted: %v", err)
	}
	defer conn.CloseNow()
	readSnapshot(t, ctx, conn)
}

// A closed browser tab must end the push loop. If it does not, every reload
// leaves another goroutine pushing snapshots into a dead connection forever.
func TestWebSocketHandlerReturnsWhenTheClientGoesAway(t *testing.T) {
	d := wsDeps()
	d.interval = 10 * time.Second
	url, returned := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	readSnapshot(t, ctx, conn)
	if err := conn.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler is still running after the client went away")
	}
}

// The socket is read-only: every write into the fleet goes through the HTTP
// routes, where one path handles validation and error reporting. A client that
// sends anything is disconnected rather than quietly ignored.
func TestWebSocketRefusesAMessageFromTheClient(t *testing.T) {
	d := wsDeps()
	d.interval = 10 * time.Second
	url, returned := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	readSnapshot(t, ctx, conn)

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"text":"do something"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Reading here both asserts what the server did with the message and plays
	// the browser's part in the close handshake, which a real client performs
	// for itself.
	_, _, err = conn.Read(ctx)
	var closed websocket.CloseError
	if !errors.As(err, &closed) {
		t.Fatalf("a client message must close the socket, got %v", err)
	}
	if closed.Code != websocket.StatusPolicyViolation {
		t.Fatalf("want a policy-violation close, got %v", closed)
	}
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler is still running after the socket was closed")
	}
}

func TestWebSocketIsUnavailableWithoutASnapshotSource(t *testing.T) {
	url, _ := wsServer(t, Deps{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url+"/ws", nil)
	if err == nil {
		conn.CloseNow()
		t.Fatal("a panel with no snapshot source must not open the socket")
	}
	if resp == nil {
		t.Fatalf("no handshake response to inspect, error was: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", resp.StatusCode)
	}
}
