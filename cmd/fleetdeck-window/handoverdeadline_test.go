//go:build darwin

package main

import (
	"flag"
	"io"
	"testing"
	"time"
)

// A window of v0.10.0 starts the new window without saying how long it gives
// the handover. Its own 2436 ms is the deadline then, whatever this build's
// handoverTimeout has become.
func TestANewWindowToldNothingKeepsToV0100sHandoverDeadline(t *testing.T) {
	if oldWindowHandoverTimeoutV0100 != 2436*time.Millisecond {
		t.Fatalf("oldWindowHandoverTimeoutV0100 = %s, want v0.10.0's 2436ms", oldWindowHandoverTimeoutV0100)
	}
	if got := oldWindowHandoverTimeout(0); got != 2436*time.Millisecond {
		t.Fatalf("told nothing, the new window keeps to %s, want 2436ms", got)
	}
	if got := oldWindowHandoverTimeout(5 * time.Second); got != 5*time.Second {
		t.Fatalf("told 5s, the new window keeps to %s", got)
	}
}

// This build tells the new window its own deadline, and the new window's flag
// reads it back as the same duration.
func TestTheNewWindowIsToldThisWindowsHandoverDeadline(t *testing.T) {
	flags := flag.NewFlagSet("fleetdeck-window", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.String("url", "", "")
	flags.String("handover", "", "")
	flags.String("canonical", "", "")
	told := flags.Duration(handoverTimeoutFlag, 0, "")
	if err := flags.Parse(newWindowArgs("http://127.0.0.1:7777/", "/Applications/fleetdeck.app", "/Applications/.fleetdeck-update/handover")); err != nil {
		t.Fatal(err)
	}
	if *told != handoverTimeout || oldWindowHandoverTimeout(*told) != handoverTimeout {
		t.Fatalf("the new window reads %s, want this window's %s", *told, handoverTimeout)
	}
}

// The deadline counts from this process's start as the kernel has it: the old
// window's clock starts only once this process has started.
func TestTheHandoverDeadlineCountsFromThisProcesssStart(t *testing.T) {
	started, err := processStart()
	if err != nil {
		t.Fatal(err)
	}
	if !started.Before(time.Now()) || time.Since(started) > 10*time.Minute {
		t.Fatalf("this process started at %s, %s ago", started, time.Since(started))
	}
	if got, want := handoverDeadline(0), started.Add(oldWindowHandoverTimeoutV0100); !got.Equal(want) {
		t.Fatalf("the deadline is %s, want %s", got, want)
	}
}
