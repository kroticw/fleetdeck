package main

// The stand's content is held to what the panel itself makes of it: the card
// parser, the job store loader and the daemon client, not the bytes written.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/daemon/daemontest"
	"github.com/kroticw/fleetdeck/internal/jobs"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/transcript"
)

func TestTheStandsBoardHasALongTitledCardInEveryStage(t *testing.T) {
	home, boardDir := t.TempDir(), t.TempDir()
	if err := layout(home, boardDir); err != nil {
		t.Fatal(err)
	}
	stages := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(boardDir, "cards", "*.md"))
	if err != nil || len(files) != len(cards) {
		t.Fatalf("cards written: %v, %v", files, err)
	}
	for _, f := range files {
		c, err := board.ParseCard(f)
		if err != nil || c.ParseError != "" {
			t.Fatalf("%s: %v, %q", f, err, c.ParseError)
		}
		if len(c.Title) < 60 {
			t.Errorf("%s: title %q is too short to wrap in a column", f, c.Title)
		}
		stages[c.Stage] = true
	}
	for _, stage := range []string{"new", "active", "review", "blocked", "done"} {
		if !stages[stage] {
			t.Errorf("no card in %s", stage)
		}
	}
}

// The orchestrator panel on a stand has to look as it does for the operator:
// with its working order on disk and a terminal it can type into, it shows
// neither the missing brief's warning nor the read-only notice above the
// terminal.
func TestTheStandsOrchestratorHasItsBriefAndItsTerminalAKey(t *testing.T) {
	home, boardDir := t.TempDir(), t.TempDir()
	if err := layout(home, boardDir); err != nil {
		t.Fatal(err)
	}
	// Where the panel looks for it: the stand's fleet has a documentation
	// directory (scripts/ci-window-stand.sh), and the brief lives in the first
	// one (orchestrator.BriefPath). Written beside the board instead, the
	// orchestrator panel said the working order was not on disk (T-091).
	panel := orchestrator.Paths{Board: boardDir, Docs: []string{filepath.Join(boardDir, "docs")}}
	if got := orchestrator.ReadBriefState(orchestrator.BriefPath(panel)); got != orchestrator.BriefOurs {
		t.Fatalf("the brief where the stand's panel looks for it reads as %v, want the wizard's own", got)
	}
	t.Setenv("HOME", home)
	if _, err := daemon.ControlKey(); err != nil {
		t.Fatalf("the stand's control key: %v", err)
	}
}

// A long-named session left unanswered inside AskUserQuestion: its row's badge,
// "silent inside AskUserQuestion", is the widest a row gets, the one that cut
// the operator's session names to "fl…". Read back through the panel's own
// transcript reader, which finds the file by the session's id.
func TestTheStandsSessionIsLeftUnansweredInsideAQuestion(t *testing.T) {
	home := t.TempDir()
	if err := layout(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var silent *session
	var id string
	for i := range sessions {
		if sessions[i].Short == silentShort {
			silent, id = &sessions[i], sessionID(i)
		}
	}
	if silent == nil || len(silent.Name) <= 60 || silent.Needs != "" || silent.Unreported {
		t.Fatalf("the silent session %+v: want a name over 60 characters and an empty needs", silent)
	}
	path, err := transcript.Locate(filepath.Join(home, ".claude", "projects"), id)
	if err != nil {
		t.Fatal(err)
	}
	voice, err := transcript.ReadVoice(path)
	if err != nil {
		t.Fatal(err)
	}
	if voice.InCall == nil || voice.InCall.Tool != "AskUserQuestion" {
		t.Fatalf("the transcript's open call: %+v, want AskUserQuestion", voice.InCall)
	}
	if since := time.Since(voice.Unanswered); since < 16*time.Minute {
		t.Fatalf("unanswered for %v, want past the panel's 16 minutes", since)
	}
}

func TestTheStandsJobStoreHoldsTheStoppedSession(t *testing.T) {
	home := t.TempDir()
	if err := layout(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	records, err := jobs.Load(filepath.Join(home, ".claude", "jobs"))
	if err != nil || len(records) != 1 || records[0].Short != stoppedShort || records[0].CWD != home {
		t.Fatalf("job store: %+v, %v", records, err)
	}
	for _, s := range sessions {
		if s.Short == stoppedShort {
			t.Fatal("the stopped session is also listed as running")
		}
	}
}

func TestTheStandsDaemonListsMoreLongNamedSessionsThanFitWithOneWaiting(t *testing.T) {
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	handle, err := handlers(t.TempDir(), hold)
	if err != nil {
		t.Fatal(err)
	}
	d := daemontest.StartTest(t, handle)
	listed, err := daemon.New(d.Socket, func() (string, error) { return "", os.ErrNotExist }).ListSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) < 8 {
		t.Fatalf("%d sessions listed, want at least 8 so the list scrolls", len(listed))
	}
	waiting, unknown, long, orchestrator := 0, 0, 0, false
	for _, s := range listed {
		switch s.Waiting() {
		case daemon.Yes:
			waiting++
		case daemon.Unknown:
			// A source that never said whether anyone waits: the row's widest
			// badge, the one that left a long name "fl…" for the operator.
			unknown++
		}
		if len(s.Name) > 60 {
			long++
		}
		orchestrator = orchestrator || s.Short == orchestratorShort
	}
	if waiting != 1 || unknown < 1 || long < 5 || !orchestrator {
		t.Fatalf("waiting %d, not reported %d, long names %d, orchestrator listed %v; want 1, at least 1, at least 5, true", waiting, unknown, long, orchestrator)
	}
}

func TestTheOrchestratorsTerminalShowsLongLinesAndAStatusLine(t *testing.T) {
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	handle, err := handlers(t.TempDir(), hold)
	if err != nil {
		t.Fatal(err)
	}
	d := daemontest.StartTest(t, handle)
	a, err := daemon.New(d.Socket, func() (string, error) { return "", os.ErrNotExist }).Attach(context.Background(), orchestratorShort, 46, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	want := screen()
	got := make([]byte, 0, len(want))
	buf := make([]byte, 1024)
	for deadline := time.Now().Add(2 * time.Second); len(got) < len(want) && time.Now().Before(deadline); {
		n, err := a.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("read: %v after %q", err, got)
		}
	}
	text := string(got)
	if !strings.Contains(text, "accept edits on") || !strings.Contains(text, "\x1b[999;1H") {
		t.Fatalf("the screen has no status line on its last row: %q", text)
	}
	longest := 0
	for _, line := range strings.Split(text, "\r\n") {
		longest = max(longest, len(line))
	}
	if longest < 120 {
		t.Fatalf("the longest line is %d bytes, too short to wrap in the orchestrator panel", longest)
	}
}

// The stand with content (scripts/ci-window-stand.sh) starts this daemon and
// pins, as its fleet's orchestrator, the session whose attach shows the
// terminal: with any other pin the orchestrator panel says nothing is pinned.
func TestTheWindowStandStartsThisDaemonAndPinsItsOrchestrator(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "ci-window-stand.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"go build -o \"$stand/standdaemon\" ./scripts/standdaemon", "\"$stand/standdaemon\" -socket", "session: " + orchestratorShort} {
		if !strings.Contains(string(script), want) {
			t.Errorf("scripts/ci-window-stand.sh lacks %q", want)
		}
	}
}

// attachText reads what the stand's daemon draws for short's terminal.
func attachText(t *testing.T, short string, want int) string {
	t.Helper()
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	handle, err := handlers(t.TempDir(), hold)
	if err != nil {
		t.Fatal(err)
	}
	d := daemontest.StartTest(t, handle)
	a, err := daemon.New(d.Socket, func() (string, error) { return "", os.ErrNotExist }).Attach(context.Background(), short, 80, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got := make([]byte, 0, want)
	buf := make([]byte, 1024)
	for deadline := time.Now().Add(2 * time.Second); len(got) < want && time.Now().Before(deadline); {
		n, err := a.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("read: %v after %q", err, got)
		}
	}
	return string(got)
}

// The card a stand opens on its document tab (FLEETDECK_STAND_OPEN=carddoc-*)
// links two documents, each signed by a different session: the one on a
// question, whose terminal the tab docks, and a working one.
func TestTheStandsCardLinksTwoDocumentsSignedByTheirSessions(t *testing.T) {
	home, boardDir := t.TempDir(), t.TempDir()
	if err := layout(home, boardDir); err != nil {
		t.Fatal(err)
	}
	c, err := board.ParseCard(filepath.Join(boardDir, "cards", docCardFile))
	if err != nil || c.ParseError != "" {
		t.Fatalf("%s: %v, %q", docCardFile, err, c.ParseError)
	}
	authors := map[string]string{askDoc: askShort, workDoc: "5e55a003"}
	for name, short := range authors {
		if !slices.Contains(c.Links, name) {
			t.Errorf("the card does not link %s: %v", name, c.Links)
		}
		path := filepath.Join(boardDir, "docs", "reports", name+".md")
		if got := board.DocSession(path); got != short {
			t.Errorf("%s is signed by %q, want %q", name, got, short)
		}
	}
	// The card names a session of its own, not its documents': a sheet that
	// failed to open the document, or to read who wrote it, would dock the
	// card's session, and the stand's gates tell the two apart.
	if c.Session == "" || c.Session == askShort || c.Session == "5e55a003" {
		t.Errorf("the card's session is %q, want one of its own, apart from its documents' authors", c.Session)
	}
}

// The session a document tab docks is inside AskUserQuestion, drawn the way
// Claude Code draws it, so the frame shows what the operator answers.
func TestTheSessionOnAQuestionShowsItsChoices(t *testing.T) {
	text := attachText(t, askShort, len(askScreen()))
	for _, want := range []string{"❯ 1.", "Type something.", "Enter to select"} {
		if !strings.Contains(text, want) {
			t.Errorf("the question's screen lacks %q: %q", want, text)
		}
	}
	if strings.Contains(attachText(t, orchestratorShort, len(screen())), "Type something.") {
		t.Error("the orchestrator's terminal shows the question too")
	}
}

// The stand's fleet reads documents from the board's docs directory, where
// layout puts them.
func TestTheWindowStandPointsItsFleetAtTheDocuments(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "ci-window-stand.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `docs:\n      paths:\n        - "%s/docs"`) {
		t.Error("scripts/ci-window-stand.sh gives the content fleet no docs paths")
	}
}
