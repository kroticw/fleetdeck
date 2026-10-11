package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/state"
)

// A start takes seconds and the daemon lists nothing until claude --bg has
// come up, so the session list has nothing to draw while the operator waits.
// The panel is the one that knows a start is under way, and says so in every
// snapshot until the dispatch has answered.

// startHeld dispatches card on h and returns once StartWork has been entered;
// release lets it answer with err, and the recorder arrives on done.
func startHeld(t *testing.T, d *Deps, err error) (h http.Handler, release func(), done <-chan *httptest.ResponseRecorder) {
	return startHeldAs(t, d, "c.md", err)
}

func startHeldAs(t *testing.T, d *Deps, card string, err error) (h http.Handler, release func(), done <-chan *httptest.ResponseRecorder) {
	t.Helper()
	entered := make(chan struct{})
	gate := make(chan struct{})
	d.StartWork = func(context.Context, orchestrator.Work) (orchestrator.Result, error) {
		close(entered)
		<-gate
		return orchestrator.Result{Session: "abc12345", OK: err == nil}, err
	}
	h = New(*d)
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"card":"`+card+`","lang":"ru"}`))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		out <- rec
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the dispatch never reached StartWork")
	}
	return h, func() { close(gate) }, out
}

func snapshotOf(t *testing.T, h http.Handler) state.Snapshot {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/snapshot", nil))
	var snap state.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("snapshot: %v: %s", err, rec.Body.String())
	}
	return snap
}

func TestTheSnapshotListsAStartUntilTheDispatchAnswers(t *testing.T) {
	for name, err := range map[string]error{"started": nil, "refused": errors.New("the daemon went away")} {
		t.Run(name, func(t *testing.T) {
			d, _, card := cardDeps(t)
			d.Snapshot = func() state.Snapshot {
				return state.Snapshot{Cards: []board.Card{{Path: card, Stage: "new"}}}
			}
			before := time.Now()
			h, release, done := startHeld(t, &d, err)

			during := snapshotOf(t, h)
			if len(during.Starting) != 1 || during.Starting[0].Card != card {
				release()
				t.Fatalf("a start under way must be in the snapshot, by its card: %+v", during.Starting)
			}
			if since := during.Starting[0].Since; since.Before(before.Add(-time.Second)) || since.After(time.Now()) {
				t.Errorf("since %v does not say when the start began", since)
			}

			release()
			<-done
			if after := snapshotOf(t, h); len(after.Starting) != 0 {
				t.Fatalf("a dispatch that answered, either way, is no start under way: %+v", after.Starting)
			}
		})
	}
}

// Every fleet's tabs read one panel: a start is said only in the view whose
// board holds the card.
func TestAStartIsListedOnlyWhereItsCardIs(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.Snapshot = func() state.Snapshot {
		return state.Snapshot{Cards: []board.Card{{Path: "/another/board/x.md", Stage: "new"}}}
	}
	h, release, done := startHeld(t, &d, nil)
	defer func() {
		release()
		<-done
	}()
	if got := snapshotOf(t, h).Starting; len(got) != 0 {
		t.Fatalf("a card from another board is no start of this view's: %+v", got)
	}
}

// The page names the card by the path the snapshot gave it. A board reached
// through a symlink resolves elsewhere, and the start must still be found by
// the path the page knows.
func TestAStartIsListedByThePathThePageSent(t *testing.T) {
	d, _, _ := cardDeps(t)
	link := filepath.Join(t.TempDir(), "board")
	if err := os.Symlink(d.BoardDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	d.BoardDir = link
	shown := filepath.Join(link, "c.md")
	d.Snapshot = func() state.Snapshot {
		return state.Snapshot{Cards: []board.Card{{Path: shown, Stage: "new"}}}
	}
	h, release, done := startHeldAs(t, &d, shown, nil)
	defer func() {
		release()
		<-done
	}()
	if got := snapshotOf(t, h).Starting; len(got) != 1 || got[0].Card != shown {
		t.Fatalf("the start must be listed under the path the page sent, %s: %+v", shown, got)
	}
}
