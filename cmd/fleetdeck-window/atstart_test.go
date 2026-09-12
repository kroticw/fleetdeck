//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

func markAt(t *testing.T, when time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "last-update-check")
	if err := os.WriteFile(path, []byte(when.Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A machine that has never asked asks. This is the first run after an
// install, and the one where a person most wants to hear that there is
// something newer.
func TestTheFirstStartAsks(t *testing.T) {
	never := filepath.Join(t.TempDir(), "last-update-check")

	if !askingIsDue(never, time.Now()) {
		t.Fatal("a machine that has never asked did not ask")
	}
}

// Opening and closing the window ten times in an afternoon is ten starts, and
// asking GitHub ten times for an answer that cannot have changed is a poll
// nobody asked for.
func TestStartingAgainTheSameDayDoesNotAsk(t *testing.T) {
	now := time.Now()

	if askingIsDue(markAt(t, now.Add(-2*time.Hour)), now) {
		t.Fatal("asked again two hours after the last time")
	}
}

func TestADayLaterItAsksAgain(t *testing.T) {
	now := time.Now()

	if !askingIsDue(markAt(t, now.Add(-25*time.Hour)), now) {
		t.Fatal("did not ask again a day later")
	}
}

// A mark nobody can read is not a reason to stop asking for good. Erring
// towards one extra question is the cheap mistake; erring towards silence is
// the expensive one, and silence is the defect this whole change is about.
func TestAMarkThatCannotBeReadDoesNotSilenceTheCheck(t *testing.T) {
	unreadable := filepath.Join(t.TempDir(), "last-update-check")
	if err := os.WriteFile(unreadable, []byte("whenever"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !askingIsDue(unreadable, time.Now()) {
		t.Fatal("an unreadable mark stopped the check")
	}
}

// A mark dated in the future -- a clock that was wrong, a file copied from
// another machine -- must not put the next check off indefinitely.
func TestAMarkFromTheFutureDoesNotSilenceTheCheck(t *testing.T) {
	now := time.Now()

	if !askingIsDue(markAt(t, now.Add(72*time.Hour)), now) {
		t.Fatal("a mark dated in the future stopped the check")
	}
}

func TestAskingIsMarkedSoTheNextStartDoesNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deeper", "last-update-check")
	now := time.Now()

	markAsked(path, now)

	if askingIsDue(path, now.Add(time.Minute)) {
		t.Fatal("the check was not marked: every start would ask")
	}
}

// The whole of the start check: it asks, and what it finds reaches the page
// as something a person can act on.
func TestAStartCheckThatFindsAVersionTellsThePage(t *testing.T) {
	src := &standInChecker{offer: "v0.4.0"}

	got := checkAtStart(context.Background(), src)

	if got == nil {
		t.Fatal("a newer version was found and the page was told nothing")
	}
	if got.Step != "available" {
		t.Fatalf("step %q, want available", got.Step)
	}
	if got.Detail != "v0.4.0" {
		t.Fatalf("detail %q, want the version that is available", got.Detail)
	}
}

// Nothing newer is not news, and a line saying so on every start is noise. The
// button is there for anybody who wants to ask.
func TestAStartCheckThatFindsNothingSaysNothing(t *testing.T) {
	if got := checkAtStart(context.Background(), &standInChecker{offer: ""}); got != nil {
		t.Fatalf("said %+v about a version that is already the newest", got)
	}
}

// A start check runs without anybody asking for it, so it must not put a
// refusal on screen either: no network at login is not something a person
// needs told. Pressing the button is what asks the question out loud.
func TestAStartCheckThatCannotReachGitHubSaysNothing(t *testing.T) {
	src := &standInChecker{err: &supervisor.ReleasesUnreachableError{URL: "https://github.com", Err: errors.New("no route")}}

	if got := checkAtStart(context.Background(), src); got != nil {
		t.Fatalf("said %+v about a check nobody asked for", got)
	}
}

type standInChecker struct {
	offer string
	err   error
}

func (s *standInChecker) Check(context.Context) (string, error) { return s.offer, s.err }

func (s *standInChecker) Stage(context.Context, string, string, func(supervisor.Progress)) (string, error) {
	return "", errors.New("a start check never stages anything")
}
