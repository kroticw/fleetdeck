package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
)

func createDeps() (Deps, *[]string) {
	d, calls := testDeps()
	d.CreateCard = func(title, zone string) (string, error) {
		*calls = append(*calls, "create:"+title+":"+zone)
		return "/b/cards/2026-09-11-a-task.md", nil
	}
	return d, calls
}

func TestCreateCardStartsACardFromATitleAndAZone(t *testing.T) {
	d, calls := createDeps()
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "create:A task:planned" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"path":"/b/cards/2026-09-11-a-task.md"`) || !strings.Contains(body, `"committed":true`) {
		t.Fatalf("the answer must name the new card and say it is committed: %s", body)
	}
}

// Title and zone, nothing more (the orchestrator's decision, 2026-09-11): a
// request that tries to set anything else is refused, not partly obeyed.
func TestCreateCardRefusesAnyFieldButTitleAndZone(t *testing.T) {
	for _, extra := range []string{`"session":"abc123"`, `"stage":"active"`, `"progress":40`, `"repo":"x"`} {
		d, calls := createDeps()
		rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned",`+extra+`}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d: %s", extra, rec.Code, rec.Body.String())
		}
		if len(*calls) != 0 {
			t.Fatalf("%s: a refused request reached the board: %v", extra, *calls)
		}
	}
}

func TestCreateCardReportsAnInvalidCardAsBadRequest(t *testing.T) {
	d, _ := createDeps()
	d.CreateCard = func(string, string) (string, error) {
		return "", fmt.Errorf("%w: unknown zone %q", board.ErrInvalidCard, "someday")
	}
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"someday"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "someday") {
		t.Fatalf("the refusal must say what was wrong: %s", rec.Body.String())
	}
}

// A board path with no cards directory is the panel's configuration being wrong,
// not the request.
func TestCreateCardReportsABoardWithNoCardsDirectoryAsUnavailable(t *testing.T) {
	d, _ := createDeps()
	d.CreateCard = func(string, string) (string, error) {
		return "", fmt.Errorf("%w: /b/cards", board.ErrNoCardsDir)
	}
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestCreateCardReportsAWriteFailureAsServerError(t *testing.T) {
	d, _ := createDeps()
	d.CreateCard = func(string, string) (string, error) { return "", errors.New("disk full") }
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
}

// The card exists once the file is written; a commit that did not happen is
// said, and the answer is still a success — creating it again would make a
// second card.
func TestCreateCardReportsAnUncommittedCardAsCreated(t *testing.T) {
	d, _ := createDeps()
	d.CreateCard = func(string, string) (string, error) {
		return "/b/cards/x.md", fmt.Errorf("%w: git commit timed out", ErrCardWrittenNotCommitted)
	}
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"committed":false`, "git commit timed out", `"path":"/b/cards/x.md"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("answer lacks %s: %s", want, body)
		}
	}
}

func TestCreateCardWithoutABoardIsUnavailable(t *testing.T) {
	d, _ := createDeps()
	d.CreateCard = nil
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestCreateCardRefusesAForeignPage(t *testing.T) {
	d, calls := createDeps()
	r := jsonRequest(http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned"}`)
	r.Header.Set("Origin", "https://evil.example")
	rec := send(d, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatalf("a foreign page created a card: %v", *calls)
	}
}
