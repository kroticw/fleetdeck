package supervisor

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// slowerListen is how long the "slower" stand-ins take to listen: longer than
// the whole handover the cases below give, and shorter than the 5 s ceiling the
// new window's keeper has of its own in updateRig. A takeover that held its
// starts to that ceiling, and not to the old window's deadline, is caught by
// the deadline running out: the old window says the new one did not finish in
// time, not that it could not take over.
const slowerListen = 4 * time.Second

// A first start too slow for what is left of the old window's deadline is
// given up on in time to say so: the new window reports failed before the
// swap, and the installed bundle and panel stay as they were. The stand-in
// takes a second to go on SIGTERM, as a real panel can while its collect cycle
// ends and as any race-built stand-in does: the failure waits on that stop.
func TestAFirstStartTooSlowForTheOldWindowsDeadlineFailsBeforeTheSwap(t *testing.T) {
	r := newUpdateRig(t)
	r.newKind = "slower-slow-term"
	u := r.update("old")
	u.HandoverTimeout = 2500 * time.Millisecond

	err := u.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "could not take over") {
		t.Fatalf("update: %v (steps %v); want the new window's own failure, reported before the old window's deadline", err, r.steps())
	}
	if steps := r.steps(); slices.Contains(steps, "handover:swapped") || slices.Contains(steps, "done") {
		t.Fatalf("steps %v: a first start that failed swapped or finished", steps)
	}
	if got := revisionIn(t, r.canonical); got != "old" {
		t.Fatalf("the canonical bundle is %q, want the old one", got)
	}
	if !waitRevision(r.url, "old", 10*time.Second) {
		t.Fatal("the old panel does not answer again after the failed handover")
	}
}

// After the swap the installed bundle is the new one, and the old window
// giving up on the handover then is the worst way an update ends: it kills a
// window that has changed the installed app, and nobody hears why. A second
// start too slow for what is left is reported failed while the old window
// still reads the handover file.
func TestASecondStartTooSlowForTheOldWindowsDeadlineIsReportedWithinIt(t *testing.T) {
	r := newUpdateRig(t)
	r.newKind = "slower-canonical-slow-term"
	u := r.update("old")
	u.HandoverTimeout = 3500 * time.Millisecond

	err := u.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "could not take over") {
		t.Fatalf("update: %v (steps %v); want the new window's own failure, reported before the old window's deadline", err, r.steps())
	}
	// Control: the case reached the start after the swap.
	if steps := r.steps(); !slices.Contains(steps, "handover:swapped") {
		t.Fatalf("steps %v: the handover failed before the swap, and this case is about the start after it", steps)
	}
}
