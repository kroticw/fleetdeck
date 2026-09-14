package supervisor

import (
	"testing"
	"time"
)

// During an update a panel start gets what the takeover has left of the old
// window's deadline, and never the window's own ceiling past it; a start that
// does not answer is killed once handoverStopGrace is over.
func TestATakeoversPanelStartsGetWhatIsLeftOfTheOldWindowsDeadline(t *testing.T) {
	const ceiling = 3 * time.Second
	now := time.Now()
	tk := &Takeover{Deadline: now.Add(2436 * time.Millisecond)}

	// Before the swap, room is kept for the swap, the staged panel's stop, and a
	// second start that fails: its stop and its report.
	want := StartLimits{
		Answer:    2436*time.Millisecond - swapReserve - stopReserve - minPanelStart - stopReserve - reportMargin,
		StopGrace: handoverStopGrace,
	}
	if got := tk.startLimitsAt(now, ceiling); got != want {
		t.Fatalf("the first start gets %+v, want %+v", got, want)
	}
	tk.phase.v.Store(afterSwap)
	want = StartLimits{Answer: 1436*time.Millisecond - stopReserve - reportMargin, StopGrace: handoverStopGrace}
	if got := tk.startLimitsAt(now.Add(time.Second), ceiling); got != want {
		t.Fatalf("the second start, a second in, gets %+v, want %+v", got, want)
	}
	if got := tk.startLimitsAt(now.Add(3*time.Second), ceiling); got.Answer != 0 {
		t.Fatalf("a start past the deadline gets %+v, want nothing to answer in", got)
	}
	tk.phase.v.Store(takeoverEnded)
	if got := tk.startLimitsAt(now.Add(time.Minute), ceiling); got != (StartLimits{Answer: ceiling, StopGrace: stopGrace}) {
		t.Fatalf("a start after the takeover gets %+v, want the window's own", got)
	}

	if got := (&Takeover{}).StartLimits(ceiling); got != (StartLimits{Answer: ceiling, StopGrace: stopGrace}) {
		t.Fatalf("a takeover with no deadline gives %+v, want the window's own", got)
	}
	if got := (&Takeover{Deadline: now.Add(time.Minute)}).startLimitsAt(now, ceiling); got.Answer != ceiling {
		t.Fatalf("a deadline a minute away gives %+v, want no more than the window's own %v to answer in", got, ceiling)
	}
}

func TestATakeoverMakesNoSwapWithoutRoomLeftToStartThePanelAgain(t *testing.T) {
	now := time.Now()
	need := swapReserve + stopReserve + minPanelStart + stopReserve + reportMargin
	if err := (&Takeover{Deadline: now.Add(need)}).roomToSwap(now); err != nil {
		t.Fatalf("with %v left: %v, want the swap made", need, err)
	}
	if err := (&Takeover{Deadline: now.Add(need - time.Millisecond)}).roomToSwap(now); err == nil {
		t.Fatalf("with %v left the swap is made, want it refused", need-time.Millisecond)
	}
	if err := (&Takeover{}).roomToSwap(now); err != nil {
		t.Fatalf("with no deadline: %v, want the swap made", err)
	}
}
