package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// handDeps is a panel wired the way the real one is for a card its session is
// keeping: the board write, the reach into the session, and the cleanup. The
// card on disk holds a stage and a session, because both are read before the
// write to know what the hand changed and whose card it is.
func handDeps(t *testing.T, stage, session string) (Deps, *[]string) {
	t.Helper()
	d, calls := testDeps()
	dir := t.TempDir()
	card := filepath.Join(dir, "c.md")
	body := fmt.Sprintf("---\nid: T-052\nstage: %s\nprogress: 80\nsession: %s\n---\n\n# A card\n", stage, session)
	if err := os.WriteFile(card, []byte(body), 0o600); err != nil {
		t.Fatalf("write card: %v", err)
	}
	d.BoardDir = dir
	d.SendToSession = func(_ context.Context, short, text string) error {
		*calls = append(*calls, "send:"+short+":"+text)
		return nil
	}
	d.CleanupSession = func(_ context.Context, a orchestrator.Accepted) (orchestrator.Result, error) {
		*calls = append(*calls, "cleanup:"+a.Session+":"+filepath.Base(a.Card))
		return orchestrator.Result{OK: true, Session: a.Session, Steps: []orchestrator.Step{{Name: "session", Note: "out"}}}, nil
	}
	return d, calls
}

func sent(calls []string) (string, bool) {
	for _, c := range calls {
		if strings.HasPrefix(c, "send:") {
			return c, true
		}
	}
	return "", false
}

func did(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func steps(t *testing.T, rec fmt.Stringer) []orchestrator.Step {
	t.Helper()
	var body struct {
		Steps []orchestrator.Step `json:"steps"`
	}
	if err := json.Unmarshal([]byte(rec.String()), &body); err != nil {
		t.Fatalf("the answer must be JSON: %v (%s)", err, rec.String())
	}
	return body.Steps
}

// The whole of part one: a stage the operator set reaches the agent that is
// keeping the card, with both stages in it, because the agent goes on writing
// the card it thinks it keeps.
func TestAStageSetByHandReachesTheSessionKeepingTheCard(t *testing.T) {
	d, calls := handDeps(t, "active", "abc12345")

	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked"}`)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("the write itself succeeded, so this is a success: %d %s", rec.Code, rec.Body.String())
	}
	message, ok := sent(*calls)
	if !ok {
		t.Fatalf("the session was told nothing: %v", *calls)
	}
	for _, want := range []string{"abc12345", "active", "blocked"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the message must carry %q: %s", want, message)
		}
	}
	if strings.Count(message, "\n") != 0 {
		t.Fatalf("a message into a session is one line, or its tail sits unsent in the prompt: %q", message)
	}
}

// The session is told in the page's language, as a worker is sent its card in
// it: the page names the language with the write.
func TestAStageSetByHandIsAnnouncedInThePagesLanguage(t *testing.T) {
	d, calls := handDeps(t, "active", "abc12345")

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked","lang":"ru"}`)
	message, ok := sent(*calls)
	if !ok {
		t.Fatalf("the session was told nothing: %v", *calls)
	}
	// Up to the card's path, which the board resolves through any symlink.
	want, _, _ := strings.Cut(orchestrator.StageByHand("ru", "", "active", "blocked"), "``")
	if !strings.Contains(message, want) {
		t.Fatalf("the message must be the Russian one:\n got %s\nwant %s…", message, want)
	}
}

// The agent writes its own card with its own editor, which never comes through
// this route — but the panel writes stage here too, on a dispatch, and that one
// goes straight to the board writer. Only a hand arrives here, and only a hand
// is announced.
func TestAFieldOtherThanStageTellsTheSessionNothing(t *testing.T) {
	d, calls := handDeps(t, "active", "abc12345")

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"100"}`)
	if _, ok := sent(*calls); ok {
		t.Fatalf("progress is not a stage: %v", *calls)
	}
}

// Writing the stage the card already holds changes nothing, and an agent told
// its stage was moved to where it already was would re-read for no reason.
func TestAStageWrittenOverItselfTellsTheSessionNothing(t *testing.T) {
	d, calls := handDeps(t, "active", "abc12345")

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"active"}`)
	if _, ok := sent(*calls); ok {
		t.Fatalf("nothing moved: %v", *calls)
	}
}

func TestACardWithNoSessionHasNobodyToTell(t *testing.T) {
	d, calls := handDeps(t, "new", "")

	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("nothing to report, so the plain success: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := sent(*calls); ok {
		t.Fatalf("there is no session on this card: %v", *calls)
	}
}

// Nothing reached the card, so there is nothing to announce: an agent told
// about a stage the board refused would re-read and find it unchanged.
func TestARefusedWriteTellsTheSessionNothing(t *testing.T) {
	d, calls := handDeps(t, "active", "abc12345")
	d.SetCardField = func(string, string, string, *string) error { return errors.New("unknown stage \"blocke\"") }

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked"}`)
	if _, ok := sent(*calls); ok {
		t.Fatalf("the write was refused: %v", *calls)
	}
}

// The board is the operator's and the write took effect; a session that could
// not be reached is a note on a success, never a failure of the move.
func TestASessionThatCannotBeReachedIsANoteOnAWriteThatHappened(t *testing.T) {
	d, _ := handDeps(t, "active", "abc12345")
	d.SendToSession = func(context.Context, string, string) error {
		return errors.New("the daemon is not running")
	}

	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a write that happened answers a success: %d %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, s := range steps(t, rec.Body) {
		if s.Name == "message" && strings.Contains(s.Error, "daemon is not running") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the operator must read why the agent was not told: %s", rec.Body.String())
	}
}

func TestAPanelWithNoReachIntoItsSessionsStillMovesTheCard(t *testing.T) {
	d, _ := handDeps(t, "active", "abc12345")
	d.SendToSession = nil

	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Part two: accepting a card is what starts the cleanup, and the operator reads
// what it did off the same answer.
func TestDoneSetByHandTidiesTheSessionAway(t *testing.T) {
	d, calls := handDeps(t, "review", "abc12345")

	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"done"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a cleanup has something to report, so it answers with it: %d %s", rec.Code, rec.Body.String())
	}
	if !did(*calls, "cleanup:abc12345:c.md") {
		t.Fatalf("the cleanup never ran: %v", *calls)
	}
	if len(steps(t, rec.Body)) == 0 {
		t.Fatalf("what the cleanup did must reach the operator: %s", rec.Body.String())
	}
}

// The session is told its card was accepted while it is still running; after
// the cleanup there is nothing left to tell.
func TestTheSessionIsToldBeforeItIsPutOut(t *testing.T) {
	d, calls := handDeps(t, "review", "abc12345")

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"done"}`)
	var order []string
	for _, c := range *calls {
		switch {
		case strings.HasPrefix(c, "send:"):
			order = append(order, "send")
		case strings.HasPrefix(c, "cleanup:"):
			order = append(order, "cleanup")
		}
	}
	if strings.Join(order, ",") != "send,cleanup" {
		t.Fatalf("a session put out first cannot be told anything: %v", order)
	}
}

func TestAStageOtherThanDoneTidiesNothingAway(t *testing.T) {
	d, calls := handDeps(t, "active", "abc12345")

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked"}`)
	if did(*calls, "cleanup:") {
		t.Fatalf("only an accepted card is tidied away: %v", *calls)
	}
}

// A card with no session names nothing to tidy away, and a cleanup started on
// one would fail over a session id nobody wrote.
func TestDoneOnACardWithNoSessionTidiesNothingAway(t *testing.T) {
	d, calls := handDeps(t, "new", "")

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"done"}`)
	if did(*calls, "cleanup:") {
		t.Fatalf("there is no session on this card: %v", *calls)
	}
}

// The card is in done either way — that write is the operator's and it took
// effect. What the cleanup could not do is a report, not a refusal of the move.
func TestACleanupThatRefusedIsReportedAndTheCardStaysAccepted(t *testing.T) {
	d, _ := handDeps(t, "review", "abc12345")
	d.CleanupSession = func(context.Context, orchestrator.Accepted) (orchestrator.Result, error) {
		return orchestrator.Result{}, errors.New("this panel is not wired to tidying a session away")
	}

	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"done"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("the card moved, so this is a success: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not wired to tidying") {
		t.Fatalf("the refusal must reach the operator: %s", rec.Body.String())
	}
}

func TestAPanelThatTidiesNothingAwayStillAcceptsTheCard(t *testing.T) {
	d, calls := handDeps(t, "review", "abc12345")
	d.CleanupSession = nil

	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"done"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the card moved and nothing else was attempted: %d %s", rec.Code, rec.Body.String())
	}
	if did(*calls, "cleanup:") {
		t.Fatalf("nothing is wired to run: %v", *calls)
	}
	if _, ok := sent(*calls); !ok {
		t.Fatalf("the session is still told its card was accepted: %v", *calls)
	}
}

// A card whose frontmatter does not parse has no stage and no session to read,
// and the write into it is refused anyway.
func TestAnUnreadableCardIsNotAnnouncedToAnyone(t *testing.T) {
	d, calls := testDeps()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.md"), []byte("no frontmatter here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.BoardDir = dir
	d.SendToSession = func(_ context.Context, short, _ string) error {
		*calls = append(*calls, "send:"+short)
		return nil
	}

	do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"blocked"}`)
	if _, ok := sent(*calls); ok {
		t.Fatalf("a card nobody can read names no session: %v", *calls)
	}
}
