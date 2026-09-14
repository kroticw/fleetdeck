package supervisor

import (
	"fmt"
	"sync/atomic"
	"time"
)

// A takeover's panel starts are held inside the old window's deadline
// (T-056, v0.10.1). The window's own ceiling for a start, 3 s, is longer than
// the whole of the 2436 ms a v0.10.0 window gives a handover, so while a
// takeover runs each start gets what is left of that deadline instead:
//
//   - the first, from the staging directory, what is left less room to swap
//     the bundle, stop the staged panel, start the panel again from the
//     installed path and say how it went. A first start too slow for that
//     fails before the swap, with the installed bundle as it was.
//   - the second, from the installed path, what is left less room to say how
//     it went. The new window reports failed while the old window still reads
//     the handover file, not after the old window has given up on it and
//     killed a window that has already swapped the bundle.
const (
	// swapReserve is the swap and LaunchServices together, measured at 19-30
	// ms on 2026-09-14, and the panel's revision read before them.
	swapReserve = 100 * time.Millisecond
	// restartReserve is the staged panel's stop at the restart: killed once
	// handoverStopGrace is over, and gone a moment after.
	restartReserve = handoverStopGrace + 50*time.Millisecond
	// minPanelStart is the least a second start is swapped for: the worst start
	// measured on 2026-09-11, 110 ms, three times, the ceiling a start had
	// before v0.10.1. The second start is the same build a moment after the
	// first, warm.
	minPanelStart = 330 * time.Millisecond
	// reportMargin is a step written to the handover file and the old window's
	// next look at it, with room to spare.
	reportMargin = 3 * handoverPoll
)

// takeoverPhase is where Takeover.Run is, for StartTimeout.
type takeoverPhase struct{ v atomic.Int32 }

const (
	beforeSwap int32 = iota
	afterSwap
	takeoverEnded
)

// StartTimeout is how long a panel the keeper starts now has to answer: what
// the takeover has left of the old window's deadline, never more than
// fallback, the window's own ceiling. With no deadline, and once Run is over,
// it is fallback.
func (t *Takeover) StartTimeout(fallback time.Duration) time.Duration {
	return t.startTimeoutAt(time.Now(), fallback)
}

func (t *Takeover) startTimeoutAt(now time.Time, fallback time.Duration) time.Duration {
	phase := t.phase.v.Load()
	if t.Deadline.IsZero() || phase == takeoverEnded {
		return fallback
	}
	left := t.Deadline.Sub(now) - reportMargin
	if phase == beforeSwap {
		left -= swapReserve + restartReserve + minPanelStart
	}
	return min(max(left, 0), fallback)
}

// roomToSwap says why the swap may not be made at now, or nil: too little of
// the old window's deadline is left to start the panel again after it and say
// how that went.
func (t *Takeover) roomToSwap(now time.Time) error {
	if t.Deadline.IsZero() {
		return nil
	}
	need := swapReserve + restartReserve + minPanelStart + reportMargin
	if left := t.Deadline.Sub(now); left < need {
		return fmt.Errorf("the new panel answered with %s of the old window's deadline left, less than the %s it takes to swap the bundle and start the panel again", left.Round(time.Millisecond), need)
	}
	return nil
}
