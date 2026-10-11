package server

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/board"
)

// T-161: a write answers once its file is on disk, its commit made afterwards
// by the board's queue, and the page hears of the write at once rather than at
// the next one-second tick.

// pushesWithin counts the snapshots the socket brings in d, after the one it
// brings on connect.
func pushesWithin(ctx context.Context, t *testing.T, conn *websocket.Conn, d time.Duration) int {
	t.Helper()
	n := 0
	for {
		rctx, cancel := context.WithTimeout(ctx, d)
		_, _, err := conn.Read(rctx)
		cancel()
		if err != nil {
			return n
		}
		n++
	}
}

func dialWS(t *testing.T, d Deps) (*websocket.Conn, context.Context) {
	t.Helper()
	url, _ := wsServer(t, d)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	readSnapshot(ctx, t, conn)
	return conn, ctx
}

func TestAnUpdateIsPushedWithoutWaitingForTheTick(t *testing.T) {
	d := wsDeps()
	d.interval = time.Hour
	updates := NewNotifier()
	d.Updates = updates
	conn, ctx := dialWS(t, d)

	start := time.Now()
	updates.Notify()
	readSnapshot(ctx, t, conn)
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Fatalf("the update was pushed after %v", took)
	}
}

// A burst — a write, its refresh, the queue saying it took the commit and
// made it — is one push: the page redraws once, not ten times.
func TestABurstOfUpdatesIsOnePush(t *testing.T) {
	d := wsDeps()
	d.interval = time.Hour
	updates := NewNotifier()
	d.Updates = updates
	conn, ctx := dialWS(t, d)

	for range 10 {
		updates.Notify()
		time.Sleep(5 * time.Millisecond)
	}
	if n := pushesWithin(ctx, t, conn, 400*time.Millisecond); n != 1 {
		t.Fatalf("10 updates in 50 ms were %d pushes, want 1", n)
	}
}

func TestPushesAreAtLeastAPushGapApart(t *testing.T) {
	d := wsDeps()
	d.interval = time.Hour
	updates := NewNotifier()
	d.Updates = updates
	conn, ctx := dialWS(t, d)

	updates.Notify()
	readSnapshot(ctx, t, conn)
	first := time.Now()
	updates.Notify()
	readSnapshot(ctx, t, conn)
	if gap := time.Since(first); gap < pushGap-10*time.Millisecond {
		t.Fatalf("two pushes %v apart, want at least %v", gap, pushGap)
	}
}

func TestACardMadeWithItsCommitQueuedSaysSo(t *testing.T) {
	d, _ := testDeps()
	d.QueuedCommits = true
	d.CreateCard = func(board.NewCard) (string, error) { return "/b/cards/T-002.md", nil }
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"x","zone":"planned"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"queued":true`) || !strings.Contains(body, `"committed":false`) {
		t.Fatalf("the answer claims a commit that has not been made: %s", body)
	}
}

func TestTheSnapshotCarriesTheBoardsCommitQueue(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.CommitState = func(dir string) board.CommitState {
		if dir != d.BoardDir {
			t.Errorf("asked for the queue of %q, want %q", dir, d.BoardDir)
		}
		return board.CommitState{Failed: 2, Error: "no PIN"}
	}
	snap := snapshotOf(t, New(d))
	if snap.BoardCommits == nil || snap.BoardCommits.Failed != 2 || snap.BoardCommits.Error != "no PIN" {
		t.Fatalf("boardCommits %+v", snap.BoardCommits)
	}
}

func TestTheSnapshotSaysNothingOfAQuietQueue(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.CommitState = func(string) board.CommitState { return board.CommitState{} }
	rec := do(d, http.MethodGet, "/api/snapshot", "")
	if strings.Contains(rec.Body.String(), "boardCommits") {
		t.Fatalf("an empty queue is news: %s", rec.Body.String())
	}
}

func TestRetryingTheBoardsCommitsAsksTheQueue(t *testing.T) {
	d, _, _ := cardDeps(t)
	var asked []string
	d.RetryCommits = func(dir string) { asked = append(asked, dir) }
	rec := do(d, http.MethodPost, "/api/board/commits/retry", `{}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(asked) != 1 || asked[0] != d.BoardDir {
		t.Fatalf("retried %q", asked)
	}
}

// The board watcher waits 300 ms for a burst of file events to end. A write the
// panel made itself needs no waiting for: the snapshot is refreshed at once.
func TestAWriteAsksForAFreshSnapshotAndARefusalDoesNot(t *testing.T) {
	d, _, _ := cardDeps(t)
	var asked atomic.Int32
	d.Changed = func() { asked.Add(1) }
	if rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"40"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	d.CreateCard = func(board.NewCard) (string, error) { return "/b/cards/T-002.md", nil }
	if rec := do(d, http.MethodPost, "/api/cards", `{"title":"x","zone":"planned"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if got := asked.Load(); got != 2 {
		t.Fatalf("two writes asked for %d refreshes", got)
	}
	d.SetCardField = func(string, string, string, *string) error { return board.ErrStale }
	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"50"}`)
	if got := asked.Load(); got != 2 {
		t.Fatal("a write that did not happen asked for a refresh")
	}
}
