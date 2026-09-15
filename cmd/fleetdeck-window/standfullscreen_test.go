//go:build darwin

package main

import (
	"testing"
	"time"
)

// A stand asked for full screen (standFullScreenEnv) enters it once both side
// surfaces have loaded, leaves it once it is in, and goes in and out a second
// time; then no more, whatever the pages do after.

// The frame is measured while the window is in full screen, the toolbar hidden
// around the measures on the second trip, all before the window leaves.
func TestAStandMeasuresTheFrameInFullScreenBeforeItLeaves(t *testing.T) {
	steps := append(append([]time.Duration{standRevealToolbarOff}, standRevealMeasures...), standRevealToolbarOn, standFullScreenLeaveAfter)
	for i := 1; i < len(steps); i++ {
		if steps[i] <= steps[i-1] {
			t.Fatalf("step %d at %v comes no later than step %d at %v: %v", i, steps[i], i-1, steps[i-1], steps)
		}
	}
}

func TestAStandNotAskedForFullScreenNeverEntersIt(t *testing.T) {
	s := newStandFullScreen(false)
	for _, surface := range sideSurfaces {
		if _, ok := s.surfaceLoaded(surface); ok {
			t.Fatalf("the %s surface loaded: asked to enter full screen on a stand that did not ask for it", surface)
		}
	}
	if _, ok := s.changed(true); ok {
		t.Fatal("asked to leave a full screen the stand never asked for")
	}
}

func TestAStandEntersFullScreenOnceBothSurfacesHaveLoaded(t *testing.T) {
	s := newStandFullScreen(true)
	if _, ok := s.surfaceLoaded("orchestrator"); ok {
		t.Fatal("asked to enter full screen with only the orchestrator surface loaded")
	}
	after, ok := s.surfaceLoaded("sessions")
	if !ok || after != standFullScreenEnterAfter {
		t.Fatalf("both surfaces loaded: enter after %v, %v; want after %v", after, ok, standFullScreenEnterAfter)
	}
	if _, ok := s.surfaceLoaded("sessions"); ok {
		t.Fatal("a surface loading again asked to enter full screen a second time")
	}
}

func TestAStandGoesInAndOutOfFullScreenTwiceAndNoMore(t *testing.T) {
	s := newStandFullScreen(true)
	s.surfaceLoaded("orchestrator")
	s.surfaceLoaded("sessions")
	if _, ok := s.changed(false); ok {
		t.Fatal("a resize before entering full screen asked for a change")
	}
	for trip := 1; trip <= standFullScreenTrips; trip++ {
		after, ok := s.changed(true)
		if !ok || after != standFullScreenLeaveAfter {
			t.Fatalf("trip %d, in full screen: leave after %v, %v; want after %v", trip, after, ok, standFullScreenLeaveAfter)
		}
		if s.trip() != trip {
			t.Fatalf("in full screen on trip %d, the stand says trip %d", trip, s.trip())
		}
		if _, ok := s.changed(true); ok {
			t.Fatalf("trip %d: full screen said again asked for a change", trip)
		}
		after, ok = s.changed(false)
		if last := trip == standFullScreenTrips; ok == last || (!last && after != standFullScreenEnterAfter) {
			t.Fatalf("trip %d, out of full screen: enter again after %v, %v; want another trip only before the last", trip, after, ok)
		}
	}
	s.surfaceLoaded("orchestrator")
	s.surfaceLoaded("sessions")
	if _, ok := s.changed(true); ok {
		t.Fatal("after its trips the stand went on another")
	}
}
