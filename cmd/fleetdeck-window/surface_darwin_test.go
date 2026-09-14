package main

// Surfaces are made and taken down once, on the main thread, in TestMain
// (menu_darwin_test.go), on a window never put on screen; the tests below only
// read what was found.

import "testing"

var surfaceResult surfaceProbe

func collectSurfaceResults() {
	surfaceResult = probeSurfacesForTest()
}

func TestASurfaceIsATransparentWebViewSharingTheBoardsProcess(t *testing.T) {
	r := surfaceResult
	if r.drawsBackground {
		t.Fatal("a surface web view must not paint a background over the glass")
	}
	if !r.sharesPool || !r.sharesStore {
		t.Fatalf("pool shared = %v, store shared = %v: a surface on its own store splits localStorage", r.sharesPool, r.sharesStore)
	}
	if r.userScripts != 2 {
		t.Fatalf("user scripts = %d, want the host script and the page load script", r.userScripts)
	}
	if r.liveAfterCreate != 1 {
		t.Fatalf("live message handlers with one surface = %d, want 1", r.liveAfterCreate)
	}
	if !r.eventsSet {
		t.Fatal("the surfaces' events have nowhere to go")
	}
}

func TestSurfacesCreatedAndDestroyedManyTimesLeaveNothingBehind(t *testing.T) {
	r := surfaceResult
	if r.liveAfterChurn != 0 {
		t.Fatalf("live message handlers after 20 rounds = %d, want 0", r.liveAfterChurn)
	}
	if r.subviewsAfterChurn != 0 {
		t.Fatalf("subviews left in the panels = %d, want 0", r.subviewsAfterChurn)
	}
	if r.classRegistrations != 1 {
		t.Fatalf("handler class registered %d times, want once per process", r.classRegistrations)
	}
}
