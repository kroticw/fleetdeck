package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
)

// jobStoreWith lays out a job store holding one session, "abc12345", whose
// state.json is the given body.
func jobStoreWith(t *testing.T, state string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "abc12345"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "abc12345", "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTheCardsOwnWorktreeWins(t *testing.T) {
	t.Parallel()
	store := jobStoreWith(t, `{"cwd":"/launch","worktreePath":"/job/tree"}`)
	got, err := reviewWorkdir(store)(context.Background(), board.Card{Session: "abc12345", Worktree: "/card/tree"})
	if err != nil || got != "/card/tree" {
		t.Fatalf("%q, %v", got, err)
	}
}

func TestTheJobsWorktreeBeatsItsLaunchDirectory(t *testing.T) {
	t.Parallel()
	store := jobStoreWith(t, `{"cwd":"/launch","worktreePath":"/job/tree"}`)
	got, err := reviewWorkdir(store)(context.Background(), board.Card{Session: "abc12345"})
	if err != nil || got != "/job/tree" {
		t.Fatalf("%q, %v", got, err)
	}
}

func TestASessionThatNeverEnteredAWorktreeIsReadWhereItWasLaunched(t *testing.T) {
	t.Parallel()
	store := jobStoreWith(t, `{"cwd":"/launch"}`)
	got, err := reviewWorkdir(store)(context.Background(), board.Card{Session: "abc12345"})
	if err != nil || got != "/launch" {
		t.Fatalf("%q, %v", got, err)
	}
}

// A session the job store does not know, with no worktree field on its card,
// is told what is missing rather than read somewhere else.
func TestASessionTheJobStoreDoesNotKnowIsToldTheWorktreeFieldIsMissing(t *testing.T) {
	t.Parallel()
	store := jobStoreWith(t, `{"cwd":"/launch"}`)
	_, err := reviewWorkdir(store)(context.Background(), board.Card{Session: "ffff9999"})
	if err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("err = %v, want one naming the card's worktree field", err)
	}
	if strings.Contains(err.Error(), "transcript") {
		t.Fatalf("err = %v: a review reads a working tree, not a transcript", err)
	}
}

// A relative worktree would be resolved against the panel's own directory,
// which is nowhere the agent works.
func TestARelativeWorktreeOnTheCardIsRefused(t *testing.T) {
	t.Parallel()
	store := jobStoreWith(t, `{"cwd":"/launch"}`)
	_, err := reviewWorkdir(store)(context.Background(), board.Card{Session: "abc12345", Worktree: "work/tree"})
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err = %v, want one saying the worktree must be absolute", err)
	}
}
