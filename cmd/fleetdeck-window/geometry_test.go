//go:build darwin

package main

import (
	"math"
	"testing"
)

// The redesign (T-226): a bar across the window's top, the capsules in it on
// the window's buttons' line, and the panels under it with 10 pt margins.
func TestTheDefaultLayoutMatchesTheDesign(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})
	want := geometry{
		// Under the 52 pt bar, 10 pt from the window's sides and bottom.
		Orchestrator: rect{X: 10, Y: 52, W: 368, H: 920},
		Sessions:     rect{X: 1154, Y: 52, W: 348, H: 920},
		// Across the bar, centred 26 from the top on the buttons' line; from the
		// margin here, where layoutFor has no buttons.
		Capsules: rect{X: 10, Y: 10, W: 1492, H: 32},
		// The board starts under the bar; a card, a session or a document opens
		// clear of the sessions panel.
		Board: insets{Top: 52, Left: 396, Right: 0, ContentRight: 358},

		OrchestratorResizable: true,
		SessionsResizable:     true,
	}
	if g != want {
		t.Fatalf("layout = %+v\nwant     %+v", g, want)
	}
}

// Out of full screen the row starts past the window's buttons; in it, at the
// margin. The panels are under the bar either way, and the folded strip keeps
// its own width: the buttons are in the bar, not over the panel.
func TestTheCapsuleRowRunsAcrossTheBarPastTheWindowsButtons(t *testing.T) {
	w := panelWidths{Orchestrator: 368, Sessions: 348, OrchestratorFolded: true}
	g := layoutPastButtons(1000, 700, w, 79)
	if g.Capsules != (rect{X: 79 + capsuleGapLeft, Y: capsuleTop, W: 1000 - panelMargin - 79 - capsuleGapLeft, H: capsuleHeight}) {
		t.Fatalf("capsule row = %+v", g.Capsules)
	}
	if g.Orchestrator.W != foldedWidth || g.Orchestrator.Y != topBarHeight {
		t.Fatalf("folded strip = %+v, want %v wide under the bar", g.Orchestrator, foldedWidth)
	}
	if got := layoutPastButtons(1000, 700, w, 0).Capsules.X; got != panelMargin {
		t.Fatalf("in full screen the row starts at %v, want the margin", got)
	}
}

func TestAFoldedSessionsPanelIsTheStripOfMarks(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true})
	if g.Sessions.W != 48 || g.Sessions.X != 1512-10-48 {
		t.Fatalf("folded sessions = %+v", g.Sessions)
	}
	if g.Board.Right != 0 || g.Board.ContentRight != 58 {
		t.Fatalf("board insets = %+v, want content clear of the strip", g.Board)
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
	// Wide enough that the board's island does not narrow the 60% panel.
	width := 1600.0
	g := layoutFor(width, 800, panelWidths{Orchestrator: 100, Sessions: 5000})
	if g.Orchestrator.H != 800-52-10 {
		t.Fatalf("orchestrator height = %v, want the window less the bar and the bottom margin", g.Orchestrator.H)
	}
	if g.Orchestrator.W != 220 {
		t.Fatalf("orchestrator = %v, want the 220 floor", g.Orchestrator.W)
	}
	// Multiplied at run time, as layoutFor does: the constant 1600*0.6 is exact
	// and the float product is not.
	if g.Sessions.W != width*maxPanelShare {
		t.Fatalf("sessions = %v, want 60%% of the window", g.Sessions.W)
	}
}

// The board's island between the panels keeps its narrowest: a window too
// narrow for both panels at their widths and the island narrows the panels on
// screen, never below their readable width and never folding them.
func TestPanelsNarrowToGiveTheBoardItsMinimum(t *testing.T) {
	g := layoutFor(1000, 700, panelWidths{Orchestrator: 368, Sessions: 348})
	if island := g.Sessions.X - (g.Orchestrator.X + g.Orchestrator.W) - 2*panelMargin; island < minCenterWidth-1e-9 {
		t.Fatalf("the board's island is %v wide, want at least %v; %+v", island, minCenterWidth, g)
	}
	if g.Orchestrator.W < minPanelWidth || g.Sessions.W < minPanelWidth {
		t.Fatalf("panels = %v and %v, want neither below %v", g.Orchestrator.W, g.Sessions.W, minPanelWidth)
	}
	if !g.OrchestratorResizable || !g.SessionsResizable {
		t.Fatal("a panel narrowed for the board is still an unfolded panel with an edge")
	}
	// In proportion to what each has above the floor: 148 and 128.
	if got, want := (368-g.Orchestrator.W)/(348-g.Sessions.W), 148.0/128.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("narrowed %v : %v, want in proportion %v", 368-g.Orchestrator.W, 348-g.Sessions.W, want)
	}
}

func TestPanelsNarrowedForTheBoardStopAtTheirReadableWidth(t *testing.T) {
	g := layoutFor(800, 700, panelWidths{Orchestrator: 368, Sessions: 348})
	if g.Orchestrator.W != minPanelWidth || g.Sessions.W != minPanelWidth {
		t.Fatalf("panels = %v and %v, want both at %v", g.Orchestrator.W, g.Sessions.W, minPanelWidth)
	}
}

func TestAWindowWideEnoughKeepsThePanelsAtTheirWidths(t *testing.T) {
	g := layoutFor(1440, 700, panelWidths{Orchestrator: 368, Sessions: 348})
	if g.Orchestrator.W != 368 || g.Sessions.W != 348 {
		t.Fatalf("panels = %v and %v, want 368 and 348", g.Orchestrator.W, g.Sessions.W)
	}
}

func TestAFoldedPanelIsNotWidenedOrNarrowedForTheBoard(t *testing.T) {
	g := layoutFor(800, 700, panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true})
	if g.Sessions.W != foldedWidth {
		t.Fatalf("folded sessions = %v, want the strip's %v", g.Sessions.W, foldedWidth)
	}
}

// The frame's own minimum width: both panels at their readable width, the
// board's island at its narrowest and the four margins around them.
func TestTheFramesMinimumWidthIsBothPanelsTheBoardAndTheMargins(t *testing.T) {
	if got := frameMinWidth(); got != 4*10+2*220+360 {
		t.Fatalf("frame minimum = %v, want %v", got, 4*10+2*220+360)
	}
}
