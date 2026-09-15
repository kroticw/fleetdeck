//go:build darwin

package main

import (
	"math"
	"testing"
)

func TestTheDefaultLayoutMatchesTheChosenDesign(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})
	want := geometry{
		Orchestrator: rect{X: 8, Y: 8, W: 368, H: 966},
		Sessions:     rect{X: 1156, Y: 8, W: 348, H: 966},
		// Centred 26 from the top, on the line of the window's buttons and of
		// both panels' head rows.
		Capsules: rect{X: 386, Y: 10, W: 758, H: 32},
		// The board runs on under the sessions glass; a card, a session or a
		// document opens clear of the panel and its margin.
		Board: insets{Top: 64, Left: 394, Right: 0, ContentRight: 356},

		OrchestratorResizable: true,
		SessionsResizable:     true,
	}
	if g != want {
		t.Fatalf("layout = %+v\nwant     %+v", g, want)
	}
}

func TestAFoldedSessionsPanelIsTheStripOfMarks(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true})
	if g.Sessions.W != 48 || g.Sessions.X != 1512-8-48 {
		t.Fatalf("folded sessions = %+v", g.Sessions)
	}
	if g.Capsules.X+g.Capsules.W != g.Sessions.X-12 {
		t.Fatalf("capsules do not follow the folded panel: %+v", g.Capsules)
	}
	if g.Board.Right != 0 || g.Board.ContentRight != 56 {
		t.Fatalf("board insets = %+v, want the board under the strip and content clear of it", g.Board)
	}
}

func TestDraggingAPanelsEdgeMovesItsWidthWithThePointerWithinLimits(t *testing.T) {
	cases := []struct {
		name          string
		side          string
		start, dx     float64
		window, width float64
	}{
		// The orchestrator's edge is its right one: right is wider.
		{"orchestrator wider", "orchestrator", 368, 40, 1512, 408},
		{"orchestrator narrower", "orchestrator", 368, -40, 1512, 328},
		// The sessions panel's edge is its left one: left is wider.
		{"sessions wider", "sessions", 348, -52, 1512, 400},
		{"sessions narrower", "sessions", 348, 48, 1512, 300},
		{"never below the floor", "orchestrator", 368, -400, 1512, 220},
		{"never above sixty percent", "sessions", 348, -2000, 1200, 720},
	}
	for _, c := range cases {
		if got := draggedWidth(c.side, c.start, c.dx, c.window); got != c.width {
			t.Errorf("%s: width = %v, want %v", c.name, got, c.width)
		}
	}
}

func TestOnlyAnUnfoldedPanelCanBeResized(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true})
	if !g.OrchestratorResizable || g.SessionsResizable {
		t.Fatalf("resizable = orchestrator %v, sessions %v; want only the unfolded one", g.OrchestratorResizable, g.SessionsResizable)
	}
}

// Each panel may take 60% of the window, which two panels cannot both have.
func TestInANarrowWindowThePanelsNeverOverlapAndTheCapsuleRowIsNeverNegative(t *testing.T) {
	for _, width := range []float64{1200, 900, 600, 400, 200} {
		for _, w := range []panelWidths{
			{Orchestrator: 5000, Sessions: 5000},
			{Orchestrator: 5000, Sessions: 5000, SessionsFolded: true},
			{Orchestrator: 368, Sessions: 348},
		} {
			g := layoutFor(width, 800, w)
			if g.Orchestrator.X+g.Orchestrator.W > g.Sessions.X {
				t.Errorf("width %v, %+v: orchestrator ends at %v, past the sessions panel's start %v", width, w, g.Orchestrator.X+g.Orchestrator.W, g.Sessions.X)
			}
			if g.Sessions.W < 0 || g.Capsules.W < 0 {
				t.Errorf("width %v, %+v: sessions width %v, capsule row width %v; want neither below 0", width, w, g.Sessions.W, g.Capsules.W)
			}
		}
	}
}

func TestAPanelNeverGoesBelowItsReadableWidthOrAboveSixtyPercent(t *testing.T) {
	width := 1200.0
	g := layoutFor(width, 800, panelWidths{Orchestrator: 100, Sessions: 5000})
	if g.Orchestrator.H != 784 {
		t.Fatalf("orchestrator height = %v, want the window less both margins", g.Orchestrator.H)
	}
	if g.Orchestrator.W != 220 {
		t.Fatalf("orchestrator = %v, want the 220 floor", g.Orchestrator.W)
	}
	// Multiplied at run time, as layoutFor does: the constant 1512*0.6 is exact
	// and the float product is not.
	if g.Sessions.W != width*maxPanelShare {
		t.Fatalf("sessions = %v, want 60%% of the window", g.Sessions.W)
	}
}

// The capsule row needs its minimum (capsules_darwin.c): a window too narrow
// for both panels at their widths and the row narrows the panels on screen,
// never below their readable width and never folding them.
func TestPanelsNarrowToGiveTheCapsuleRowItsMinimum(t *testing.T) {
	g := layoutWithRow(1000, 700, panelWidths{Orchestrator: 368, Sessions: 348}, 360)
	if g.Capsules.W < 360-1e-9 {
		t.Fatalf("capsule row = %v wide, want at least its minimum 360; %+v", g.Capsules.W, g)
	}
	if g.Orchestrator.W < minPanelWidth || g.Sessions.W < minPanelWidth {
		t.Fatalf("panels = %v and %v, want neither below %v", g.Orchestrator.W, g.Sessions.W, minPanelWidth)
	}
	if !g.OrchestratorResizable || !g.SessionsResizable {
		t.Fatal("a panel narrowed for the row is still an unfolded panel with an edge")
	}
	if math.Abs(g.Orchestrator.X+g.Orchestrator.W+capsuleGapLeft-g.Capsules.X) > 1e-9 || math.Abs(g.Capsules.X+g.Capsules.W+capsuleGapRight-g.Sessions.X) > 1e-9 {
		t.Fatalf("the row is not between the narrowed panels: %+v", g)
	}
}

func TestPanelsNarrowedForTheRowStopAtTheirReadableWidth(t *testing.T) {
	g := layoutWithRow(800, 700, panelWidths{Orchestrator: 368, Sessions: 348}, 360)
	if g.Orchestrator.W != minPanelWidth || g.Sessions.W != minPanelWidth {
		t.Fatalf("panels = %v and %v, want both at %v", g.Orchestrator.W, g.Sessions.W, minPanelWidth)
	}
}

func TestAWindowWideEnoughKeepsThePanelsAtTheirWidths(t *testing.T) {
	w := panelWidths{Orchestrator: 368, Sessions: 348}
	if got, want := layoutWithRow(1440, 700, w, 360), layoutFor(1440, 700, w); got != want {
		t.Fatalf("layout = %+v\nwant     %+v", got, want)
	}
}

func TestAFoldedPanelIsNotWidenedOrNarrowedForTheRow(t *testing.T) {
	g := layoutWithRow(800, 700, panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true}, 360)
	if g.Sessions.W != foldedWidth {
		t.Fatalf("folded sessions = %v, want the strip's %v", g.Sessions.W, foldedWidth)
	}
	if g.Capsules.W < 360-1e-9 {
		t.Fatalf("capsule row = %v, want at least 360", g.Capsules.W)
	}
}

// v0.10.2's dev build on macOS 27 (the operator's frame 1374): with the
// orchestrator panel folded to its strip, the capsule row began 10 pt past the
// strip, under the window's buttons, which end 79 pt in. The row begins its gap
// past the buttons; the strip and the board stay where they were.
func TestTheCapsuleRowBesideAFoldedOrchestratorBeginsPastTheWindowsButtons(t *testing.T) {
	folded := panelWidths{Orchestrator: 368, Sessions: 348, OrchestratorFolded: true}
	g := layoutPastButtons(1000, 700, folded, 0, 79)
	if g.Capsules.X != 79+capsuleGapLeft || g.Capsules.X+g.Capsules.W != g.Sessions.X-capsuleGapRight {
		t.Fatalf("capsule row = %+v, want it from %v to the sessions panel's gap", g.Capsules, 79+capsuleGapLeft)
	}
	if g.Orchestrator.W != foldedWidth || g.Board.Left != panelMargin+foldedWidth+boardGapLeft {
		t.Fatalf("the strip %+v or the board's left inset %v moved with the row", g.Orchestrator, g.Board.Left)
	}
	if got, want := layoutPastButtons(1000, 700, folded, 0, 0), layoutFor(1000, 700, folded); got != want {
		t.Fatalf("with no buttons on the row's line: %+v\nwant %+v", got, want)
	}
	unfolded := panelWidths{Orchestrator: 368, Sessions: 348}
	if got, want := layoutPastButtons(1000, 700, unfolded, 360, 79), layoutWithRow(1000, 700, unfolded, 360); got != want {
		t.Fatalf("an unfolded orchestrator panel ends past the buttons: %+v\nwant %+v", got, want)
	}
}

// Beside the folded strip the room the buttons take is room the row has not:
// the sessions panel narrows for the row's minimum past them.
func TestTheSessionsPanelNarrowsForTheRowPastTheWindowsButtons(t *testing.T) {
	g := layoutPastButtons(800, 700, panelWidths{Orchestrator: 368, Sessions: 500, OrchestratorFolded: true}, 360, 79)
	if g.Capsules.X != 79+capsuleGapLeft || g.Capsules.W < 360-1e-9 {
		t.Fatalf("capsule row = %+v, want it from %v and at least 360 wide", g.Capsules, 79+capsuleGapLeft)
	}
}

// The window's minimum width, less the row's: both panels at their readable
// width, the margins and the row's gaps to them.
func TestTheFramesPartOfTheMinimumWidthIsBothPanelsMarginsAndGaps(t *testing.T) {
	if got, want := frameMinWidth(), 2*panelMargin+2*minPanelWidth+capsuleGapLeft+capsuleGapRight; got != want {
		t.Fatalf("frame minimum = %v, want %v", got, want)
	}
}
