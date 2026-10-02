package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

const cleanupUUID = "11111111-2222-3333-4444-555555555555"

// cleanupStand is a panel's outside world for one cleanup: a job store holding
// the session, its transcript, and a board with a card and an archive.
type cleanupStand struct {
	deps    cleanupDeps
	board   string
	card    string
	stopped []string
}

func standFor(t *testing.T, said string) *cleanupStand {
	t.Helper()
	root := t.TempDir()
	jobStore := filepath.Join(root, "jobs", "abc12345")
	if err := os.MkdirAll(jobStore, 0o755); err != nil {
		t.Fatal(err)
	}
	state := fmt.Sprintf(`{"sessionId":%q,"name":"T-052 stage by hand","cwd":%q}`, cleanupUUID, root)
	if err := os.WriteFile(filepath.Join(jobStore, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	projects := filepath.Join(root, "projects", "-a-repo")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	if said != "" {
		line := fmt.Sprintf(`{"type":"assistant","timestamp":"2026-09-24T10:00:00Z","message":{"content":[{"type":"text","text":%q}]}}`, said)
		if err := os.WriteFile(filepath.Join(projects, cleanupUUID+".jsonl"), []byte(line+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	board := filepath.Join(root, "board")
	if err := os.MkdirAll(filepath.Join(board, "cards"), 0o755); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(board, "cards", "T-052.md")
	if err := os.WriteFile(card, []byte("---\nid: T-052\nstage: done\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &cleanupStand{board: board, card: card}
	s.deps = cleanupDeps{
		jobStore: filepath.Join(root, "jobs"),
		projects: filepath.Join(root, "projects"),
		board:    board,
		list: func(context.Context) ([]daemon.Session, error) {
			return []daemon.Session{{Short: "abc12345"}}, nil
		},
		stop: func(_ context.Context, short string) error {
			s.stopped = append(s.stopped, short)
			return nil
		},
		now: func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) },
	}
	return s
}

func (s *cleanupStand) archive(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(s.board, "archive", "AGENTS-ARCHIVE.md"))
	if err != nil {
		return ""
	}
	return string(body)
}

// The whole of part two through the panel's own wiring: what the session said
// is read off its transcript, recorded in the board's archive under its name
// and its card, and only then is the session put out.
func TestTheCleanupRecordsWhatTheSessionSaidBeforePuttingItOut(t *testing.T) {
	s := standFor(t, "ветка запушена, MR !7")

	res, err := newCleaner(s.deps).Cleanup(context.Background(), orchestrator.Accepted{Session: "abc12345", Card: s.card})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !res.OK {
		t.Fatalf("every step was wired: %+v", res.Steps)
	}
	entry := s.archive(t)
	for _, want := range []string{"2026-09-24", "abc12345", "T-052 stage by hand", "cards/T-052.md", "ветка запушена", cleanupUUID} {
		if !strings.Contains(entry, want) {
			t.Fatalf("the archive entry must carry %q:\n%s", want, entry)
		}
	}
	if len(s.stopped) != 1 || s.stopped[0] != "abc12345" {
		t.Fatalf("the session must be put out, once: %v", s.stopped)
	}
}

// A session with no transcript has nothing to take off it, and putting it out
// would be the loss the order of these steps exists to prevent.
func TestTheCleanupLeavesASessionWithNoTranscriptRunning(t *testing.T) {
	s := standFor(t, "")

	res, err := newCleaner(s.deps).Cleanup(context.Background(), orchestrator.Accepted{Session: "abc12345", Card: s.card})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(s.stopped) != 0 {
		t.Fatalf("nothing was read off it, so it stays: %v", s.stopped)
	}
	if res.OK {
		t.Fatalf("this is not a finished cleanup: %+v", res.Steps)
	}
	if entry := s.archive(t); entry != "" {
		t.Fatalf("nothing was recorded, so the archive is untouched:\n%s", entry)
	}
}

// The store knows a session by its short id and the transcript by its UUID.
// Reading the wrong one archives someone else's words, so a session the store
// does not know is a failure and not an empty entry.
func TestTheCleanupRefusesASessionTheJobStoreDoesNotKnow(t *testing.T) {
	s := standFor(t, "done")

	res, err := newCleaner(s.deps).Cleanup(context.Background(), orchestrator.Accepted{Session: "ffff9999", Card: s.card})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if res.OK || len(s.stopped) != 0 {
		t.Fatalf("a session nothing is known about must not be put out: %+v %v", res.Steps, s.stopped)
	}
}

// A session is put out with the claude the panel starts sessions with, as
// `claude stop <short>`: on a stand that is the stand's own claude and never
// the one on PATH, which would reach the operator's real fleet.
func TestAStandStopsSessionsOnlyWithItsOwnClaude(t *testing.T) {
	onPath, ranOnPath := claudeThatRecords(t, "11111111")
	t.Setenv("PATH", filepath.Dir(onPath))
	standBin, recorded := claudeThatRecordsArgs(t, "")

	stop := sessionStopper(runOpts{standSocket: "/tmp/no.sock", standClaude: standBin}, nil)
	if stop == nil {
		t.Fatal("a stand with its own claude cannot stop sessions")
	}
	if err := stop(context.Background(), "abc12345"); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(recorded)
	if got := strings.Split(strings.TrimSpace(string(args)), "\n"); !slices.Equal(got, []string{"stop", "abc12345"}) {
		t.Errorf("stop arguments = %q", got)
	}
	if ran(ranOnPath) {
		t.Error("a stand ran the claude on PATH")
	}
}

// A stand given no claude stops nothing, as it starts nothing: a done card on
// a stand must not put out a session of the operator's own. With no way to
// stop, the fleet has no cleanup at all, and a card moved into done leaves its
// session running.
func TestAStandWithoutItsOwnClaudeStopsNoSession(t *testing.T) {
	onPath, ranOnPath := claudeThatRecords(t, "0a1b2c3d")
	t.Setenv("PATH", filepath.Dir(onPath))
	o := runOpts{standSocket: "/tmp/no.sock"}
	if stop := sessionStopper(o, nil); stop != nil {
		_ = stop(context.Background(), "abc12345")
		t.Error("a stand given no claude of its own can stop sessions")
	}
	if fleetCleaner(o, nil, t.TempDir(), nil, t.TempDir()) != nil {
		t.Error("a stand given no claude has a cleanup that stops sessions")
	}
	if ran(ranOnPath) {
		t.Error("a stand ran the claude on PATH")
	}
}

// A fleet with no board has nowhere to record the session it would put out,
// so it has no cleanup.
func TestAFleetWithoutABoardHasNoCleanup(t *testing.T) {
	standBin, _ := claudeThatRecordsArgs(t, "")
	if fleetCleaner(runOpts{standSocket: "/tmp/no.sock", standClaude: standBin}, nil, "", nil, t.TempDir()) != nil {
		t.Error("a fleet with no board has a cleanup")
	}
}

// A configured command is how another installation is reached, so a session
// is stopped through it as written, flags and all.
func TestAConfiguredCommandStopsSessionsAsWritten(t *testing.T) {
	bin, recorded := claudeThatRecordsArgs(t, "")
	if err := sessionStopper(runOpts{}, []string{bin, "--flag"})(context.Background(), "abc12345"); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(recorded)
	if got := strings.Split(strings.TrimSpace(string(args)), "\n"); !slices.Equal(got, []string{"--flag", "stop", "abc12345"}) {
		t.Errorf("stop arguments = %q", got)
	}
}

// A stop claude refused is reported with claude's own words: they are what the
// operator reads in the cleanup's report.
func TestAFailedStopSaysWhatClaudeSaid(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'no session abc12345' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := sessionStopper(runOpts{}, []string{bin})(context.Background(), "abc12345")
	if err == nil || !strings.Contains(err.Error(), "no session abc12345") {
		t.Errorf("err = %v, want claude's own words", err)
	}
}
