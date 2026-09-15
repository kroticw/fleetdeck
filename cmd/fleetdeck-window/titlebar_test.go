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

// The buttons measured with the orchestrator panel folded reach past its strip:
// the frame is laid out again with the capsule row past them, and the board
// told its insets, once.
func TestButtonsMeasuredBesideAFoldedOrchestratorLayTheFrameOutAgain(t *testing.T) {
	c := loadedFrame()
	c.panel("orchestrator", true)
	w := panelWidths{Orchestrator: 368, Sessions: 348, OrchestratorFolded: true}
	want := []effect{
		applyGeometry{G: layoutPastButtons(1512, 982, w, 0, 79)},
		boardInsets(),
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "titlebar", "inset": 79 - panelMargin + titlebarGap, "center": 26 - panelMargin}},
	}
	got := c.titlebarButtons(79, 26)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v\nwant      %#v", got, want)
	}
	if g := geometryOf(t, got); g.Capsules.X != 79+capsuleGapLeft {
		t.Fatalf("capsule row from %v, want past the zoom button at %v", g.Capsules.X, 79+capsuleGapLeft)
	}
	if got := c.titlebarButtons(79, 26); len(got) != 0 {
		t.Fatalf("the same measure again: %#v, want nothing", got)
	}
	if got := c.titlebarButtons(79, 20); len(titlebarMessages(got)) != 1 || len(got) != 1 {
		t.Fatalf("buttons moved down the title bar only: %#v, want only the surface told", got)
	}
}

// Folding the orchestrator panel with its buttons already measured lays the
// row out past them.
func TestFoldingTheOrchestratorPanelKeepsTheRowPastTheWindowsButtons(t *testing.T) {
	c := loadedFrame()
	c.titlebarButtons(79, 26)
	if g := geometryOf(t, c.panel("orchestrator", true)); g.Capsules.X != 79+capsuleGapLeft {
		t.Fatalf("capsule row from %v, want past the zoom button at %v", g.Capsules.X, 79+capsuleGapLeft)
	}
}

// In full screen the buttons are not over the capsule row: beside the folded
// strip the row keeps its gap to the strip, and has it past the buttons again
// out of full screen.
func TestInFullScreenTheRowBesideAFoldedOrchestratorKeepsToTheStrip(t *testing.T) {
	c := loadedFrame()
	c.titlebarButtons(79, 26)
	c.panel("orchestrator", true)
	if g := geometryOf(t, c.resized(1440, 900, true)); g.Capsules.X != panelMargin+foldedWidth+capsuleGapLeft {
		t.Fatalf("in full screen the row is from %v, want %v past the strip", g.Capsules.X, panelMargin+foldedWidth+capsuleGapLeft)
	}
	if g := geometryOf(t, c.resized(1000, 700, false)); g.Capsules.X != 79+capsuleGapLeft {
		t.Fatalf("out of full screen the row is from %v, want past the zoom button at %v", g.Capsules.X, 79+capsuleGapLeft)
	}
}

// Dragging the sessions panel wide beside the folded strip stops where the row
// keeps its minimum past the buttons.
func TestDraggingTheSessionsPanelBesideAFoldedOrchestratorLeavesTheRowItsMinimumPastTheButtons(t *testing.T) {
	c := loadedFrame()
	c.titlebarButtons(79, 26)
	c.panel("orchestrator", true)
	c.capsuleRow(360)
	c.resized(1000, 700, false)
	start, ok := c.resizeStart("sessions")
	if !ok {
		t.Fatal("no edge to drag")
	}
	if g := geometryOf(t, c.resizeTo("sessions", start, -2000)); g.Capsules.X != 79+capsuleGapLeft || g.Capsules.W < 360-1e-9 {
		t.Fatalf("dragged wide: row %+v, want it from %v and at least 360 wide", g.Capsules, 79+capsuleGapLeft)
	}
	// The width kept is the one the frame shows, not one the row narrows again.
	want := 1000 - 2*panelMargin - capsuleGapLeft - capsuleGapRight - 360 - (79 - panelMargin)
	saved := c.resizeEnd()
	if len(saved) == 0 || saved[0] != (saveWidths{W: panelWidths{Orchestrator: 368, Sessions: want, OrchestratorFolded: true}}) {
		t.Fatalf("on release: %#v, want the sessions panel saved at %v", saved, want)
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
