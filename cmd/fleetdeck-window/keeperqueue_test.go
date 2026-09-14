package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// collect runs q into a slice until want events have come or a second has
// passed.
func collect(t *testing.T, q *keeperQueue, want int) []supervisor.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []supervisor.Event
	go q.run(ctx, func(e supervisor.Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= want {
			break
		}
	}
	// Anything past want would be a duplicate: give it a moment to show.
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return append([]supervisor.Event(nil), got...)
}

func TestTheKeepersWordBeforeTheWindowIsReadyReachesItAllInOrderOnce(t *testing.T) {
	q := newKeeperQueue()
	said := []supervisor.Event{
		{State: supervisor.Starting, PID: 1},
		{State: supervisor.Answering, Ours: true, PID: 1, Took: time.Second},
		{State: supervisor.Starting, PID: 2},
		{State: supervisor.Failed, Err: errors.New("the panel ended")},
	}
	for _, e := range said {
		q.push(e)
	}
	got := collect(t, q, len(said))
	if len(got) != len(said) {
		t.Fatalf("the window heard %d events, want the keeper's %d: %+v", len(got), len(said), got)
	}
	for i := range said {
		if got[i].State != said[i].State || got[i].PID != said[i].PID {
			t.Fatalf("event %d is %+v, want %+v: the order the keeper said them in", i, got[i], said[i])
		}
	}
}

func TestTheKeepersWordAfterTheWindowIsReadyIsHeardToo(t *testing.T) {
	q := newKeeperQueue()
	q.push(supervisor.Event{State: supervisor.Starting, PID: 7})
	go func() {
		time.Sleep(30 * time.Millisecond)
		q.push(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 7})
	}()
	got := collect(t, q, 2)
	if len(got) != 2 || got[0].State != supervisor.Starting || got[1].State != supervisor.Answering {
		t.Fatalf("heard %+v, want starting then answering", got)
	}
}

// A panel that fails before the window has a web view is not left behind a
// "starting" page: the failure, heard after the start, puts up its own page.
func TestAPanelThatFailedBeforeTheWindowWasReadyShowsTheFailure(t *testing.T) {
	q := newKeeperQueue()
	q.push(supervisor.Event{State: supervisor.Starting, PID: 3})
	q.push(supervisor.Event{State: supervisor.Failed, Err: errors.New("nothing answered")})
	scr := &screen{url: "http://127.0.0.1:7777/", logPath: "/tmp/fleetdeck.log", now: time.Now}
	var pages []string
	for _, e := range collect(t, q, 2) {
		if _, page := scr.on(e); page != "" {
			pages = append(pages, pageHeading(page))
		}
	}
	if len(pages) != 2 || pages[1] != pageHeading(failedPage(scr.url, supervisor.Event{State: supervisor.Failed, Err: errors.New("nothing answered")}, scr.logPath)) {
		t.Fatalf("pages put up: %q; want the starting page, then the failure page", pages)
	}
}

func TestOnlyAStandThatAsksStartsThePanelFirst(t *testing.T) {
	asks := standSettings{panelFirst: true}
	cases := []struct {
		name     string
		stand    standSettings
		handover string
		action   runAction
		want     bool
	}{
		{"a person's window", standSettings{}, "", runHere, false},
		{"a stand that asks", asks, "", runHere, true},
		{"a takeover on that stand", asks, "/tmp/handover", runHere, false},
		{"from a staging directory on that stand", asks, "", runRefused, false},
	}
	for _, c := range cases {
		if got := startsPanelFirst(c.stand, c.handover, c.action); got != c.want {
			t.Errorf("%s: starts the panel first = %v, want %v", c.name, got, c.want)
		}
	}
}
