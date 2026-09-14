package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func waitOn(events ...Event) error {
	ch := make(chan Event, len(events))
	for _, e := range events {
		ch <- e
	}
	t := &Takeover{URL: "http://127.0.0.1:1/", Events: ch}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return t.waitOwnAnswer(ctx)
}

// An answer left over from the panel started before -- the staged one, while
// the takeover waits for the canonical one -- is not taken for the answer it
// waits for.
func TestATakeoverDoesNotTakeAnEarlierPanelsAnswerForItsOwn(t *testing.T) {
	err := waitOn(
		Event{State: Answering, Ours: true, PID: 1},
	)
	if err == nil {
		t.Fatal("the takeover took an answer from a panel it had not started in this wait")
	}
	err = waitOn(
		Event{State: Answering, Ours: true, PID: 1},
		Event{State: Starting, PID: 2},
		Event{State: Answering, Ours: true, PID: 1},
		Event{State: Answering, Ours: true, PID: 2},
	)
	if err != nil {
		t.Fatalf("the takeover did not take its own panel's answer after the old ones: %v", err)
	}
}

func TestATakeoverRefusesAPanelItDidNotStart(t *testing.T) {
	err := waitOn(Event{State: Starting, PID: 3}, Event{State: Answering, Ours: false})
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("err = %v, want the takeover to refuse a panel it did not start", err)
	}
}
