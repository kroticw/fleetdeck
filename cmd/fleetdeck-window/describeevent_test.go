package main

import (
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// How long a start really took is in the log, and not only whether it made
// the deadline: the deadline is derived from these.
func TestTheLogSaysHowLongThePanelTookToAnswer(t *testing.T) {
	got := describeEvent(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 4242, Took: 1234567 * time.Microsecond})
	if !strings.Contains(got, "pid 4242") || !strings.Contains(got, "answered 1.235s after it started") {
		t.Fatalf("describeEvent = %q, want the pid and the time to answer", got)
	}
}
