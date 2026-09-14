package main

// The stand's content is held to what the panel itself makes of it: the card
// parser, the job store loader and the daemon client, not the bytes written.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/daemon/daemontest"
	"github.com/kroticw/fleetdeck/internal/jobs"
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
	waiting, long, orchestrator := 0, 0, false
	for _, s := range listed {
		if s.Waiting() == daemon.Yes {
			waiting++
		}
		if len(s.Name) > 60 {
			long++
		}
		orchestrator = orchestrator || s.Short == orchestratorShort
	}
	if waiting != 1 || long < 5 || !orchestrator {
		t.Fatalf("waiting %d, long names %d, orchestrator listed %v; want 1, at least 5, true", waiting, long, orchestrator)
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
