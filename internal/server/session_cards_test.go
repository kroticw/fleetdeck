package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/fleet"
)

func sessionCardsBoard(t *testing.T, title string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cards", "T-001-2026-09-11-a.md"),
		"---\nid: T-001\nzone: planned\nstage: active\nprogress: 20\nsession: abc12345\nrepo: x\ncreated: 2026-09-11\n---\n\n# "+title+"\n")
	writeFile(t, filepath.Join(dir, "archive", "2026-09-10-b.md"),
		"---\nid: T-000\nzone: planned\nstage: done\nprogress: 100\nsession: abc12345\nrepo: x\ncreated: 2026-09-10\n---\n\n# closed "+title+"\n")
	return dir
}

type sessionCardAnswer struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Stage    string `json:"stage"`
	Created  string `json:"created"`
	Path     string `json:"path"`
	Archived bool   `json:"archived"`
}

func decodeSessionCards(t *testing.T, body string) []sessionCardAnswer {
	t.Helper()
	var got []sessionCardAnswer
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("not a list of cards: %v (%s)", err, body)
	}
	return got
}

func TestSessionCardsRouteAnswersTheSessionsHistoryClosedWorkIncluded(t *testing.T) {
	d, _ := testDeps()
	d.BoardDir = sessionCardsBoard(t, "work")

	rec := do(d, http.MethodGet, "/api/sessions/abc12345/cards", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeSessionCards(t, rec.Body.String())
	if len(got) != 2 || got[0].ID != "T-000" || !got[0].Archived || got[1].ID != "T-001" || got[1].Stage != "active" {
		t.Fatalf("history = %+v", got)
	}
	if got[1].Title != "work" || got[1].Created != "2026-09-11" || got[1].Path == "" {
		t.Fatalf("fields missing: %+v", got[1])
	}
	// A history is a list of names, not the cards: the bodies stay on the board.
	if strings.Contains(rec.Body.String(), "\"body\"") {
		t.Fatalf("card bodies were served: %s", rec.Body.String())
	}
}

func TestSessionCardsRouteOfASessionThatTookNothingIsAnEmptyList(t *testing.T) {
	d, _ := testDeps()
	d.BoardDir = sessionCardsBoard(t, "work")

	rec := do(d, http.MethodGet, "/api/sessions/ffff0000/cards", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	// [] and not null: the page draws "none" from an empty list, and null is
	// a shape it would have to guess about.
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("body = %q, want []", rec.Body.String())
	}
}

func TestSessionCardsRouteWithoutABoardSaysSo(t *testing.T) {
	d, _ := testDeps()
	rec := do(d, http.MethodGet, "/api/sessions/abc12345/cards", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}

	d.BoardDir = t.TempDir() // no cards/ in it: board.path names the wrong directory
	rec = do(d, http.MethodGet, "/api/sessions/abc12345/cards", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

func TestSessionCardsRouteReadsTheBoardOfTheFleetTheTabNames(t *testing.T) {
	d, _ := testDeps()
	first := sessionCardsBoard(t, "first fleet's")
	second := sessionCardsBoard(t, "second fleet's")
	d.BoardDir = first
	d.Fleet = func(name string) (FleetDeps, error) {
		switch name {
		case "":
			return FleetDeps{BoardDir: first}, nil
		case "B":
			return FleetDeps{BoardDir: second}, nil
		}
		return FleetDeps{}, errors.Join(fleet.ErrUnknown, errors.New(name))
	}

	rec := do(d, http.MethodGet, "/api/sessions/abc12345/cards?fleet=B", "")
	got := decodeSessionCards(t, rec.Body.String())
	if len(got) != 2 || got[1].Title != "second fleet's" {
		t.Fatalf("history = %+v", got)
	}

	rec = do(d, http.MethodGet, "/api/sessions/abc12345/cards?fleet=nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown fleet: status = %d", rec.Code)
	}
}
