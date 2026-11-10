//go:build darwin

package main

import (
	"reflect"
	"testing"
)

// The title bar's buttons sit at the left of the bar across the window's top;
// the capsule row starts past the zoom button's right edge, which the window
// measures (frame.titlebarInset).

func TestTheButtonsMeasuredLayTheFrameOutAgainWithTheRowPastThem(t *testing.T) {
	c := loadedFrame()
	want := []effect{
		applyGeometry{G: layoutPastButtons(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348}, 79)},
		boardInsets(),
	}
	got := c.titlebarButtons(79)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v\nwant      %#v", got, want)
	}
	if g := geometryOf(t, got); g.Capsules.X != 79+capsuleGapLeft {
		t.Fatalf("capsule row from %v, want past the zoom button at %v", g.Capsules.X, 79+capsuleGapLeft)
	}
	if got := c.titlebarButtons(79); len(got) != 0 {
		t.Fatalf("the same measure again: %#v, want nothing", got)
	}
}

// The panels are under the bar: no page has the buttons over it, so none is
// told of them.
func TestNoSurfaceIsToldOfTheTitleBar(t *testing.T) {
	c := started()
	c.layout(1, "panel", "work")
	c.titlebarButtons(79)
	for _, surface := range []string{"orchestrator", "sessions"} {
		for _, e := range c.pageLoaded(surface, "panel") {
			if m, ok := e.(sendTo); ok && m.Message["type"] == "titlebar" {
				t.Fatalf("%s told of the title bar: %#v", surface, m)
			}
		}
	}
}

// In full screen the buttons are not on the bar: the row starts at the margin,
// and past the buttons again out of full screen.
func TestInFullScreenTheRowStartsAtTheMargin(t *testing.T) {
	c := loadedFrame()
	c.titlebarButtons(79)
	if g := geometryOf(t, c.resized(1440, 900, true)); g.Capsules.X != panelMargin {
		t.Fatalf("in full screen the row is from %v, want %v", g.Capsules.X, panelMargin)
	}
	if g := geometryOf(t, c.resized(1000, 700, false)); g.Capsules.X != 79+capsuleGapLeft {
		t.Fatalf("out of full screen the row is from %v, want past the zoom button at %v", g.Capsules.X, 79+capsuleGapLeft)
	}
}

// The measure itself, on a titled window with the frame in (frame_darwin.c):
// the three buttons sit at the left of the bar, well short of the
// orchestrator panel's width.
func TestTheZoomButtonEndsAtTheBarsLeft(t *testing.T) {
	if x := frameResult.titlebarInset; x < 40 || x > 120 {
		t.Fatalf("the zoom button ends %v pt from the window's left edge, want between 40 and 120", x)
	}
}
