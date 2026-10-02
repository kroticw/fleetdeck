package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/state"
)

// The panel rebuilds its snapshot every couple of seconds and pushes once a
// second, so without this every second push is a byte-for-byte copy of the one
// before it: the same JSON encoded again, sent again, and applied to the DOM
// again for nothing.
func TestWebSocketSkipsUnchangedSnapshot(t *testing.T) {
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	d := Deps{
		interval: 10 * time.Millisecond,
		Snapshot: func() state.Snapshot {
			return state.Snapshot{At: at, Cards: []board.Card{{Path: "/b/c.md", Stage: "active"}}}
		},
	}
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	if got := readSnapshot(ctx, t, conn); !got.At.Equal(at) {
		t.Fatalf("first push must carry the snapshot, got %v", got.At)
	}

	quiet, cancelQuiet := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelQuiet()
	if _, _, err := conn.Read(quiet); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a snapshot that has not changed must not be pushed again, got %v", err)
	}
}

// A panel that has not built its first snapshot yet reports a zero moment, and
// the page still has to be told what little there is — otherwise it sits blank
// with no way to tell a starting panel from a broken socket.
func TestWebSocketPushesSnapshotWithZeroMoment(t *testing.T) {
	d := wsDeps()
	d.interval = 10 * time.Second
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()

	if got := readSnapshot(ctx, t, conn); len(got.Cards) != 1 {
		t.Fatalf("the first push must go out even with a zero At, got %+v", got)
	}
}
