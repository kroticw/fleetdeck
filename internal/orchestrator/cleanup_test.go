package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/daemon"
)

// cleanerLog is what a cleanup did, in the order it did it, so a test can say
// something about the order and not only about the outcome.
type cleanerLog struct {
	steps    []string
	archived string
}

func testCleaner(log *cleanerLog) *Cleaner {
	return &Cleaner{
		Unpin: func(string) error {
			log.steps = append(log.steps, "pin")
			return nil
		},
		Words: func(string) (Said, error) {
			log.steps = append(log.steps, "words")
			return Said{Words: "the branch is pushed"}, nil
		},
		Archive: func(_ Accepted, said Said) error {
			log.steps = append(log.steps, "archive")
			log.archived = said.Words
			return nil
		},
		Stop: func(context.Context, string) error {
			log.steps = append(log.steps, "stop")
			return nil
		},
		List: func(context.Context) ([]daemon.Session, error) {
			return []daemon.Session{{Short: "abc12345"}}, nil
		},
	}
}

var accepted = Accepted{Session: "abc12345", Card: "/b/cards/T-052.md"}

func stepNamed(res Result, name string) (Step, bool) {
	for _, s := range res.Steps {
		if s.Name == name {
			return s, true
		}
	}
	return Step{}, false
}

func TestCleanupTakesTheKnowledgeOffTheSessionBeforePuttingItOut(t *testing.T) {
	var log cleanerLog
	res, err := testCleaner(&log).Cleanup(context.Background(), accepted)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !res.OK {
		t.Fatalf("every step was done, so the cleanup is done: %+v", res.Steps)
	}
	if got := strings.Join(log.steps, ","); got != "pin,words,archive,stop" {
		t.Fatalf("the order is the whole point of this sequence, got %s", got)
	}
	if log.archived != "the branch is pushed" {
		t.Fatalf("what the session said must reach the archive, got %q", log.archived)
	}
	if res.Session != accepted.Session {
		t.Fatalf("the result must name the session it was about, got %q", res.Session)
	}
}

// The one failure this whole sequence exists to prevent: a session put out with
// its knowledge still only inside it.
func TestCleanupLeavesTheSessionRunningWhenItsWordsCannotBeRead(t *testing.T) {
	var log cleanerLog
	c := testCleaner(&log)
	c.Words = func(string) (Said, error) { return Said{}, errors.New("no transcript for abc12345") }

	res, err := c.Cleanup(context.Background(), accepted)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if res.OK {
		t.Fatal("a cleanup that kept the session running is not done")
	}
	if strings.Contains(strings.Join(log.steps, ","), "stop") {
		t.Fatalf("the session must still be running: %v", log.steps)
	}
	step, ok := stepNamed(res, "words")
	if !ok || step.Error == "" {
		t.Fatalf("the operator has to read why nothing was put out: %+v", res.Steps)
	}
	if !strings.Contains(step.Error, "no transcript") {
		t.Fatalf("the reason must come through as it was: %q", step.Error)
	}
}

func TestCleanupLeavesTheSessionRunningWhenTheArchiveCannotBeWritten(t *testing.T) {
	var log cleanerLog
	c := testCleaner(&log)
	c.Archive = func(Accepted, Said) error { return errors.New("archive is read-only") }

	res, err := c.Cleanup(context.Background(), accepted)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if res.OK {
		t.Fatal("nothing was recorded, so the cleanup is not done")
	}
	if strings.Contains(strings.Join(log.steps, ","), "stop") {
		t.Fatalf("an unrecorded session must keep running: %v", log.steps)
	}
	if step, ok := stepNamed(res, "archive"); !ok || step.Error == "" {
		t.Fatalf("the archive failure must be in the result: %+v", res.Steps)
	}
}

// A pin left behind costs a line at the top of a list; the knowledge is still
// taken off the session and the session is still put out.
func TestCleanupCarriesOnWhenThePinCannotBeDropped(t *testing.T) {
	var log cleanerLog
	c := testCleaner(&log)
	c.Unpin = func(string) error { return errors.New("the pin set is locked") }

	res, err := c.Cleanup(context.Background(), accepted)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if got := strings.Join(log.steps, ","); got != "words,archive,stop" {
		t.Fatalf("everything after the pin must still happen, got %s", got)
	}
	step, ok := stepNamed(res, "pin")
	if !ok || step.Error == "" {
		t.Fatalf("a pin that stayed behind must be said out loud: %+v", res.Steps)
	}
	if res.OK {
		t.Fatal("a cleanup with a step that failed is not a clean one")
	}
}

// Accepting a card whose session died hours ago is ordinary, and the knowledge
// is archived all the same — but the panel must not report putting out
// something that was not there.
func TestCleanupSaysThereWasNothingToPutOut(t *testing.T) {
	var log cleanerLog
	c := testCleaner(&log)
	c.List = func(context.Context) ([]daemon.Session, error) { return nil, nil }

	res, err := c.Cleanup(context.Background(), accepted)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if strings.Contains(strings.Join(log.steps, ","), "stop") {
		t.Fatalf("nothing is running, so nothing may be stopped: %v", log.steps)
	}
	if !strings.Contains(strings.Join(log.steps, ","), "archive") {
		t.Fatalf("a dead session's knowledge is archived like any other: %v", log.steps)
	}
	step, ok := stepNamed(res, "session")
	if !ok || step.Error == "" {
		t.Fatalf("a cleanup that put out nothing must say so: %+v", res.Steps)
	}
	if res.OK {
		t.Fatal("silence about a session that was already gone is the failure this test is for")
	}
}

// A session that is dying is on its way out under someone else's hand: asking
// for it to be stopped again is asking the daemon about a job that is going.
func TestCleanupTreatsADyingSessionAsGone(t *testing.T) {
	var log cleanerLog
	c := testCleaner(&log)
	c.List = func(context.Context) ([]daemon.Session, error) {
		return []daemon.Session{{Short: "abc12345", Dying: true}}, nil
	}

	res, _ := c.Cleanup(context.Background(), accepted)
	if strings.Contains(strings.Join(log.steps, ","), "stop") {
		t.Fatalf("a session already going does not need putting out: %v", log.steps)
	}
	if step, ok := stepNamed(res, "session"); !ok || step.Error == "" {
		t.Fatalf("the operator must be told: %+v", res.Steps)
	}
}

// A list that cannot be read says nothing about the session, and reading it as
// "not there" would leave a live session running with its card accepted.
func TestCleanupStopsTheSessionWhenTheListCannotBeRead(t *testing.T) {
	var log cleanerLog
	c := testCleaner(&log)
	c.List = func(context.Context) ([]daemon.Session, error) { return nil, errors.New("daemon unavailable") }

	res, err := c.Cleanup(context.Background(), accepted)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if !strings.Contains(strings.Join(log.steps, ","), "stop") {
		t.Fatalf("an unknown answer is not a no: %v", log.steps)
	}
	if !res.OK {
		t.Fatalf("the session was put out, so the cleanup is done: %+v", res.Steps)
	}
}

func TestCleanupReportsAStopThatFailed(t *testing.T) {
	var log cleanerLog
	c := testCleaner(&log)
	c.Stop = func(context.Context, string) error { return errors.New("claude is not installed") }

	res, err := c.Cleanup(context.Background(), accepted)
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if res.OK {
		t.Fatal("the session is still running")
	}
	if step, ok := stepNamed(res, "session"); !ok || !strings.Contains(step.Error, "claude is not installed") {
		t.Fatalf("the runtime's own words must reach the operator: %+v", res.Steps)
	}
}

func TestCleanupRefusesARequestThatNamesNoSession(t *testing.T) {
	var log cleanerLog
	if _, err := testCleaner(&log).Cleanup(context.Background(), Accepted{Card: "/b/c.md"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
	if len(log.steps) != 0 {
		t.Fatalf("a refused request does nothing: %v", log.steps)
	}
}

func TestCleanupRefusesARequestThatNamesNoCard(t *testing.T) {
	var log cleanerLog
	if _, err := testCleaner(&log).Cleanup(context.Background(), Accepted{Session: "abc12345"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
	if len(log.steps) != 0 {
		t.Fatalf("a refused request does nothing: %v", log.steps)
	}
}
