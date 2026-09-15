//go:build darwin

package main

import "testing"

// A stand asked for full screen (standFullScreenEnv) enters it once both side
// surfaces have loaded, and leaves it once it is in: one trip, whatever the
// pages do after.

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

func TestAStandLeavesFullScreenOnceItIsInAndGoesNoMoreTrips(t *testing.T) {
	s := newStandFullScreen(true)
	s.surfaceLoaded("orchestrator")
	s.surfaceLoaded("sessions")
	if _, ok := s.changed(false); ok {
		t.Fatal("a resize before entering full screen asked to leave it")
	}
	after, ok := s.changed(true)
	if !ok || after != standFullScreenLeaveAfter {
		t.Fatalf("in full screen: leave after %v, %v; want after %v", after, ok, standFullScreenLeaveAfter)
	}
	if _, ok := s.changed(true); ok {
		t.Fatal("full screen said again asked to leave a second time")
	}
	s.changed(false)
	s.surfaceLoaded("orchestrator")
	s.surfaceLoaded("sessions")
	if _, ok := s.changed(true); ok {
		t.Fatal("after the trip the stand went on another")
	}
}
