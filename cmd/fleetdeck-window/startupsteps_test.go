//go:build darwin

package main

import (
	"bytes"
	"log"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"
)

// Each step of the start is said with how long after the process started it
// was done, counted from the kernel's start time, not from when the step was
// asked to be said.
func TestAStartStepIsSaidWithTheTimeSinceTheProcessStarted(t *testing.T) {
	var out bytes.Buffer
	defer log.SetOutput(log.Writer())
	log.SetOutput(&out)
	started, err := processStart()
	if err != nil {
		t.Fatal(err)
	}

	step := startupSteps()
	step("the web view is made")

	m := regexp.MustCompile(`fleetdeck-window: the web view is made, (\d+) ms after the process started`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("the log says %q", out.String())
	}
	ms, _ := strconv.ParseInt(m[1], 10, 64)
	if since := time.Since(started).Milliseconds(); ms > since || ms < since-1000 {
		t.Fatalf("the step is said %d ms after the process started, and the process started %d ms ago", ms, since)
	}
}

// A build made to measure the start (lsprobe.go, built with -tags lsprobe) is
// handed each step as it is said, in order, so that it can look at the system
// at that moment. An ordinary build hands it to nothing.
func TestAStartStepIsHandedToTheStartProbe(t *testing.T) {
	defer log.SetOutput(log.Writer())
	log.SetOutput(&bytes.Buffer{})
	was := startupProbe
	defer func() { startupProbe = was }()
	var got []string
	startupProbe = func(step string) { got = append(got, step) }

	step := startupSteps()
	step("started")
	step("the web view is made")

	if want := []string{"started", "the web view is made"}; !slices.Equal(got, want) {
		t.Fatalf("the probe was handed %q, want %q", got, want)
	}
}
