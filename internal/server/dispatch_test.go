package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

func TestDispatchHandsTheCardOverAndReportsItsSteps(t *testing.T) {
	d, _, card := cardDeps(t)
	var got orchestrator.Work
	d.StartWork = func(_ context.Context, w orchestrator.Work) (orchestrator.Result, error) {
		got = w
		return orchestrator.Result{Session: "abc12345", OK: true, Steps: []orchestrator.Step{{Name: "session", Note: "started"}}}, nil
	}
	rec := do(d, http.MethodPost, "/api/sessions", `{"card":"c.md","lang":"ru"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got.Card != card {
		t.Fatalf("the dispatch got %q, want the resolved card %q", got.Card, card)
	}
	if got.Lang != "ru" {
		t.Fatalf("the page's language did not reach the dispatch: lang = %q", got.Lang)
	}
	if !strings.Contains(rec.Body.String(), `"abc12345"`) {
		t.Fatalf("the started session must reach the page: %s", rec.Body.String())
	}
}

// The path arrives from the browser and names a file the dispatch writes into.
// Confining it is the same rule a card write lives by, not a nicety.
func TestDispatchIsConfinedToTheBoard(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.StartWork = func(context.Context, orchestrator.Work) (orchestrator.Result, error) {
		t.Fatal("a card outside the board reached the dispatch")
		return orchestrator.Result{}, nil
	}
	rec := do(d, http.MethodPost, "/api/sessions", `{"card":"../outside.md","lang":"ru"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDispatchMapsItsRefusalsOntoStatusCodes(t *testing.T) {
	cases := map[error]int{
		orchestrator.ErrBadRequest:         http.StatusBadRequest,
		orchestrator.ErrBusy:               http.StatusConflict,
		orchestrator.ErrCardTaken:          http.StatusConflict,
		orchestrator.ErrNoCard:             http.StatusNotFound,
		orchestrator.ErrCannotStart:        http.StatusServiceUnavailable,
		errors.New("the daemon went away"): http.StatusInternalServerError,
	}
	for err, want := range cases {
		t.Run(err.Error(), func(t *testing.T) {
			d, _, _ := cardDeps(t)
			d.StartWork = func(context.Context, orchestrator.Work) (orchestrator.Result, error) {
				return orchestrator.Result{}, err
			}
			rec := do(d, http.MethodPost, "/api/sessions", `{"card":"c.md","lang":"ru"}`)
			if rec.Code != want {
				t.Fatalf("want %d, got %d: %s", want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDispatchOnAPanelThatStartsNoWorkerSaysSo(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.StartWork = nil
	rec := do(d, http.MethodPost, "/api/sessions", `{"card":"c.md","lang":"ru"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The page offers a start only where one can succeed, and reads that off the
// snapshot: a panel wired to start no worker (a stand with no claude) says so
// by the field's absence rather than by a start that fails with 503.
func TestTheSnapshotSaysWhetherAWorkerCanBeStarted(t *testing.T) {
	d, _ := testDeps()
	d.StartWork = func(context.Context, orchestrator.Work) (orchestrator.Result, error) {
		return orchestrator.Result{}, nil
	}
	if rec := do(d, http.MethodGet, "/api/snapshot", ""); !strings.Contains(rec.Body.String(), `"canStartWork":true`) {
		t.Fatalf("a panel that can start a worker must say so: %s", rec.Body.String())
	}
	d.StartWork = nil
	if rec := do(d, http.MethodGet, "/api/snapshot", ""); strings.Contains(rec.Body.String(), `canStartWork`) {
		t.Fatalf("a panel that starts no worker must not offer one: %s", rec.Body.String())
	}
}

// With several fleets the answer is the fleet's the tab shows, asked the way
// every write route asks it.
func TestTheSnapshotAsksTheFleetWhetherAWorkerCanBeStarted(t *testing.T) {
	d, _ := testDeps()
	d.Fleet = func(string) (FleetDeps, error) {
		return FleetDeps{StartWork: func(context.Context, orchestrator.Work) (orchestrator.Result, error) {
			return orchestrator.Result{}, nil
		}}, nil
	}
	if rec := do(d, http.MethodGet, "/api/snapshot", ""); !strings.Contains(rec.Body.String(), `"canStartWork":true`) {
		t.Fatalf("the fleet can start a worker and the snapshot must say so: %s", rec.Body.String())
	}
}
