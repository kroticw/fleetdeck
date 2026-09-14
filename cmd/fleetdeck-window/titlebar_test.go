//go:build darwin

package main

import (
	"reflect"
	"testing"
)

// The title bar's buttons float over the orchestrator panel's top corner. The
// orchestrator's surface is told how far into it they reach, so its header
// starts past them; the window measures the zoom button's right edge
// (frame.titlebarInset) and the controller turns it into the surface's inset.

func titlebarMessages(effects []effect) []map[string]any {
	var out []map[string]any
	for _, e := range effects {
		if m, ok := e.(sendTo); ok && m.Message["type"] == "titlebar" {
			if m.Surface != "orchestrator" {
				panic("a titlebar message to " + m.Surface)
			}
			out = append(out, m.Message)
		}
	}
	return out
}

func TestTheOrchestratorSurfaceLearnsWhereTheTitleBarButtonsEndWhenItLoads(t *testing.T) {
	c := started()
	c.layout(1, "panel", "work")
	if got := titlebarMessages(c.titlebarInset(76)); len(got) != 0 {
		t.Fatalf("before the surface loaded: %v, want nothing sent", got)
	}
	want := []map[string]any{{"type": "titlebar", "inset": 76 - panelMargin + titlebarGap}}
	if got := titlebarMessages(c.pageLoaded("orchestrator", "panel")); !reflect.DeepEqual(got, want) {
		t.Fatalf("on the orchestrator's load: %v, want %v", got, want)
	}
	if got := titlebarMessages(c.pageLoaded("sessions", "panel")); len(got) != 0 {
		t.Fatalf("the sessions surface was told of the title bar: %v", got)
	}
}

func TestTheOrchestratorSurfaceHearsOfTheTitleBarAgainOnlyWhenItMoves(t *testing.T) {
	c := loadedFrame()
	if got := titlebarMessages(c.titlebarInset(76)); len(got) != 1 {
		t.Fatalf("the first measure after load: %v, want one message", got)
	}
	if got := titlebarMessages(c.titlebarInset(76)); len(got) != 0 {
		t.Fatalf("the same measure again: %v, want nothing", got)
	}
	want := []map[string]any{{"type": "titlebar", "inset": 90 - panelMargin + titlebarGap}}
	if got := titlebarMessages(c.titlebarInset(90)); !reflect.DeepEqual(got, want) {
		t.Fatalf("a moved button: %v, want %v", got, want)
	}
}

// The measure itself, on a titled window with the frame in (frame_darwin.c):
// the three buttons sit at the left, so the zoom button ends well inside the
// orchestrator panel and well short of its width.
func TestTheZoomButtonEndsInsideTheOrchestratorPanel(t *testing.T) {
	if x := frameResult.titlebarInset; x < 40 || x > 120 {
		t.Fatalf("the zoom button ends %v pt from the window's left edge, want between 40 and 120", x)
	}
}

// No buttons -- a window without a title bar, or one whose buttons are hidden
// -- is no inset: the header keeps its own padding.
func TestNoTitleBarButtonsIsNoInset(t *testing.T) {
	c := loadedFrame()
	c.titlebarInset(76)
	want := []map[string]any{{"type": "titlebar", "inset": 0.0}}
	if got := titlebarMessages(c.titlebarInset(0)); !reflect.DeepEqual(got, want) {
		t.Fatalf("buttons gone: %v, want %v", got, want)
	}
}
