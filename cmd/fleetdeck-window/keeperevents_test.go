//go:build darwin

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// A panel that fails before the window has a web view is not left behind a
// "starting" page: the failure, heard after the start, puts up its own page.
func TestAPanelThatFailedBeforeTheWindowWasReadyShowsTheFailure(t *testing.T) {
	events := supervisor.NewKeeperEvents()
	failure := supervisor.Event{State: supervisor.Failed, Err: errors.New("nothing answered")}
	events.Push(supervisor.Event{State: supervisor.Starting, PID: 3})
	events.Push(failure)

	scr := &screen{url: "http://127.0.0.1:7777/", logPath: "/tmp/fleetdeck.log", now: time.Now}
	pages := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go events.Run(ctx, func(e supervisor.Event) {
		if _, page := scr.on(e); page != "" {
			pages <- pageHeading(page)
		}
	})

	want := []string{pageHeading(startingPage(scr.url, false)), pageHeading(failedPage(scr.url, failure, scr.logPath))}
	for i, heading := range want {
		select {
		case got := <-pages:
			if got != heading {
				t.Fatalf("page %d is %q, want %q", i, got, heading)
			}
		case <-time.After(time.Second):
			t.Fatalf("page %d never came; want %q", i, heading)
		}
	}
}
