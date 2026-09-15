//go:build darwin

package main

import (
	"reflect"
	"testing"
)

// The title bar's buttons sit in the orchestrator panel's top corner. The
// orchestrator's surface is told how far into it they reach, so its header
// starts past them, and the line they are centred on, so its header row is
// centred on it too; the window measures the zoom button's right edge and the
// buttons' centre (frame.titlebarInset, frame.titlebarCenter) and the
// controller turns them into the surface's coordinates.

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

func TestTheOrchestratorSurfaceLearnsWhereTheTitleBarButtonsAreWhenItLoads(t *testing.T) {
	c := started()
	c.layout(1, "panel", "work")
	if got := titlebarMessages(c.titlebarButtons(79, 26)); len(got) != 0 {
		t.Fatalf("before the surface loaded: %v, want nothing sent", got)
	}
	want := []map[string]any{{"type": "titlebar", "inset": 79 - panelMargin + titlebarGap, "center": 26 - panelMargin}}
	if got := titlebarMessages(c.pageLoaded("orchestrator", "panel")); !reflect.DeepEqual(got, want) {
		t.Fatalf("on the orchestrator's load: %v, want %v", got, want)
	}
	if got := titlebarMessages(c.pageLoaded("sessions", "panel")); len(got) != 0 {
		t.Fatalf("the sessions surface was told of the title bar: %v", got)
	}
}

func TestTheOrchestratorSurfaceHearsOfTheTitleBarAgainOnlyWhenItsButtonsMove(t *testing.T) {
	c := loadedFrame()
	if got := titlebarMessages(c.titlebarButtons(79, 26)); len(got) != 1 {
		t.Fatalf("the first measure after load: %v, want one message", got)
	}
	if got := titlebarMessages(c.titlebarButtons(79, 26)); len(got) != 0 {
		t.Fatalf("the same measure again: %v, want nothing", got)
	}
	want := []map[string]any{{"type": "titlebar", "inset": 79 - panelMargin + titlebarGap, "center": 20 - panelMargin}}
	if got := titlebarMessages(c.titlebarButtons(79, 20)); !reflect.DeepEqual(got, want) {
		t.Fatalf("buttons moved down the title bar: %v, want %v", got, want)
	}
	want = []map[string]any{{"type": "titlebar", "inset": 90 - panelMargin + titlebarGap, "center": 20 - panelMargin}}
	if got := titlebarMessages(c.titlebarButtons(90, 20)); !reflect.DeepEqual(got, want) {
		t.Fatalf("buttons moved along it: %v, want %v", got, want)
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
// -- is no inset and no line: the header keeps its own padding.
func TestNoTitleBarButtonsIsNoInsetAndNoLine(t *testing.T) {
	c := loadedFrame()
	c.titlebarButtons(79, 26)
	want := []map[string]any{{"type": "titlebar", "inset": 0.0, "center": 0.0}}
	if got := titlebarMessages(c.titlebarButtons(0, 0)); !reflect.DeepEqual(got, want) {
		t.Fatalf("buttons gone: %v, want %v", got, want)
	}
}
