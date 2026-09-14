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

// The frame takes the board out of the window's content view, where T-059's
// navigation delegate first looked for it: without WebKit's word the board's
// page would be asked for again on the old short waits.
func TestTheBoardUnderTheFrameStillHasWebKitsWordOnItsNavigations(t *testing.T) {
	if !surfaceResult.boardObserved {
		t.Fatal("the board's web view under the glass frame takes no navigation delegate")
	}
}

// A surface's delegate reports what WebKit says of its navigations, as the
// board's does: with a delegate set, WebKit no longer reloads a page whose
// process went away, and a surface would stay blank.
func TestASurfaceReportsItsNavigationsAsTheBoardDoes(t *testing.T) {
	if !surfaceResult.reportsNavigation {
		t.Fatal("a surface's navigation delegate does not report commits, failures and a lost web content process")
	}
}

func TestAClickInThePanelReachesItsSurfaceAndOnlyTheEdgeIsTheStrips(t *testing.T) {
	if !surfaceResult.clickInPanelReachesSurface {
		t.Fatalf("a click in the middle of the sessions panel does not reach its web view: it lands on %s; the web view's frame is %+v",
			surfaceResult.clickInPanelLandsOn, surfaceResult.surfaceFrame)
	}
	if !surfaceResult.clickOnEdgeReachesStrip {
		t.Fatal("a click on the sessions panel's edge does not reach the width strip")
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
