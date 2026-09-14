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
//     installed path, and fail that start and say so. A first start too slow
//     for that fails before the swap, with the installed bundle as it was.
//   - the second, from the installed path, what is left less room to fail and
//     say so. The new window reports failed while the old window still reads
//     the handover file, not after the old window has given up on it and
//     killed a window that has already swapped the bundle.
//
// Failing a start takes time too: the panel that did not answer is stopped
// before the failure is reported. During a takeover it is killed once
// handoverStopGrace is over rather than given stopGrace, 6 s, to shut down, and
// that stop is inside the room kept. Found on CI, 2026-09-14: a race-built
// stand-in takes a second to exit on SIGTERM, and the failure it was stopped
// for reached the old window after its deadline. A real panel told to stop can
// wait as long on its collect cycle (T-060).
const (
	// swapReserve is the swap and LaunchServices together, measured at 19-30
	// ms on 2026-09-14, and the panel's revision read before them.
	swapReserve = 100 * time.Millisecond
	// stopReserve is one panel stopped during a takeover -- the staged one at
	// the restart, or one that did not answer in time: killed once
	// handoverStopGrace is over, and gone a moment after.
	stopReserve = handoverStopGrace + 50*time.Millisecond
	// minPanelStart is the least a second start is swapped for: the worst start
	// measured on 2026-09-11, 110 ms, three times, the ceiling a start had
	// before v0.10.1. The second start is the same build a moment after the
	// first, warm.
	minPanelStart = 330 * time.Millisecond
	// reportMargin is a step written to the handover file and the old window's
	// next look at it, with room to spare.
	reportMargin = 3 * handoverPoll
)

const (
	// afterFailedStart is what a start that fails needs past its ceiling: the
	// panel stopped and the failure reported.
	afterFailedStart = stopReserve + reportMargin
	// swapRoom is what the swap needs left after the first start: the swap, the
	// staged panel stopped, and a second start that may fail.
	swapRoom = swapReserve + stopReserve + minPanelStart + afterFailedStart
)

// StartLimits is how one start of the keeper's panel is bounded.
type StartLimits struct {
	// Answer is how long the panel has to answer.
	Answer time.Duration
	// StopGrace is how long a panel that did not answer in time has to go on
	// SIGTERM before it is killed.
	StopGrace time.Duration
}

// takeoverPhase is where Takeover.Run is, for StartLimits.
type takeoverPhase struct{ v atomic.Int32 }

const (
	beforeSwap int32 = iota
	afterSwap
	takeoverEnded
)

// StartLimits is how a panel the keeper starts now is bounded: what the
// takeover has left of the old window's deadline to answer in, never more than
// fallback, the window's own ceiling, and handoverStopGrace to go if it does
// not. With no deadline, and once Run is over, they are the window's own:
// fallback, and stopGrace.
func (t *Takeover) StartLimits(fallback time.Duration) StartLimits {
	return t.startLimitsAt(time.Now(), fallback)
}

func (t *Takeover) startLimitsAt(now time.Time, fallback time.Duration) StartLimits {
	phase := t.phase.v.Load()
	if t.Deadline.IsZero() || phase == takeoverEnded {
		return StartLimits{Answer: fallback, StopGrace: stopGrace}
	}
	left := t.Deadline.Sub(now) - afterFailedStart
	if phase == beforeSwap {
		left = t.Deadline.Sub(now) - swapRoom
	}
	return StartLimits{Answer: min(max(left, 0), fallback), StopGrace: handoverStopGrace}
}

// roomToSwap says why the swap may not be made at now, or nil: too little of
// the old window's deadline is left to start the panel again after it, and to
// fail that start and say so.
func (t *Takeover) roomToSwap(now time.Time) error {
	if t.Deadline.IsZero() {
		return nil
	}
	if left := t.Deadline.Sub(now); left < swapRoom {
		return fmt.Errorf("the new panel answered with %s of the old window's deadline left, less than the %s it takes to swap the bundle and start the panel again", left.Round(time.Millisecond), swapRoom)
	}
	return nil
}
