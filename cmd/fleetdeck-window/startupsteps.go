//go:build darwin

package main

import (
	"log"
	"time"
)

// startupSteps says in the window's log how long after this process started
// each step of the window's start was done. A handover has the old window's
// whole deadline, 2436 ms for a v0.10.0 window, to reach done, and a first
// start of a fresh bundle on a macOS 26 runner spent 1615 ms of it between the
// start of main and the panel starting (T-064, run 34858260944). The lines are
// kept in every start: the operator's log is where a slow start is found.
func startupSteps() func(step string) {
	started, err := processStart()
	if err != nil {
		log.Printf("fleetdeck-window: this process's start time is unknown (%v); the start's steps are timed from here", err)
		started = time.Now()
	}
	return func(step string) {
		log.Printf("fleetdeck-window: %s, %d ms after the process started", step, time.Since(started).Milliseconds())
	}
}
