package main

// The band is probed once, on the main thread, in TestMain
// (menu_darwin_test.go), on a window never put on screen that counts what it is
// asked to do; the tests below only read what it found.

import "testing"

var bandResult bandProbe

func collectBandResults() {
	bandResult = probeBandForTest(frameGeometry)
}

const (
	drag = iota
	zoom
	fill
	minimize
)

// v0.10.0 could not be moved: the board runs under the title bar and takes
// every press there.
func TestAPressAtTheWindowsEmptyTopDragsTheWindow(t *testing.T) {
	r := bandResult
	if !r.bandTakesTheTop {
		t.Fatal("a press 30 pt from the top, over the board, does not land on the band")
	}
	if r.bandWidth != 1512 {
		t.Fatalf("band is %v pt wide, want the window's 1512", r.bandWidth)
	}
	if r.press != [4]int{drag: 1} {
		t.Fatalf("a press on the band asked the window for %v (drag, zoom, fill, minimize), want one drag", r.press)
	}
}

func TestTheBandLeavesThePanelsTheCapsulesAndTheBoardBelowIt(t *testing.T) {
	r := bandResult
	if !r.surfaceOverTheBand {
		t.Fatal("a press on the orchestrator panel's top lands on the band, not its surface")
	}
	if !r.capsuleOverTheBand {
		t.Fatal("a press on a capsule lands on the band, not the capsule")
	}
	if !r.boardBelowTheBand {
		t.Fatal("a press just under the band does not reach the board")
	}
}

// The page says how much of its top is empty; what is not, the page keeps.
func TestTheBandIsOnlyAsTallAsThePageSays(t *testing.T) {
	r := bandResult
	if !r.bandAboveAShortBand || !r.boardBelowAShortBand {
		t.Fatalf("on a 20 pt band: band at 10 pt %v, board at 30 pt %v; want both", r.bandAboveAShortBand, r.boardBelowAShortBand)
	}
	if !r.boardWithNoBand {
		t.Fatal("with no band a press at the top does not reach the board")
	}
}

func TestADoubleClickOnTheBandDoesWhatTheSystemSettingSays(t *testing.T) {
	// Before macOS 15 there is no Fill, and the setting cannot say it.
	fills := [4]int{zoom: 1}
	if bandResult.hasFill {
		fills = [4]int{fill: 1}
	}
	want := map[string][4]int{
		"Maximize": {zoom: 1},
		"Fill":     fills,
		"Minimize": {minimize: 1},
		"None":     {},
	}
	for action, calls := range want {
		// The double click's first press is a drag, as on a title bar.
		if got := bandResult.doubleClick[action]; got != calls {
			t.Errorf("setting %q: a double click asked for %v (drag, zoom, fill, minimize), want %v", action, got, calls)
		}
	}
}

func TestTheBandDoesNothingInFullScreen(t *testing.T) {
	if bandResult.fullScreen != [4]int{} {
		t.Fatalf("in full screen a press and a double click asked for %v (drag, zoom, fill, minimize), want nothing", bandResult.fullScreen)
	}
}
