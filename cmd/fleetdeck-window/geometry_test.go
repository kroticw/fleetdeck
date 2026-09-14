//go:build darwin

package main

import "testing"

func TestTheDefaultLayoutMatchesTheChosenDesign(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})
	want := geometry{
		Orchestrator: rect{X: 8, Y: 8, W: 368, H: 966},
		Sessions:     rect{X: 1156, Y: 8, W: 348, H: 966},
		Capsules:     rect{X: 386, Y: 12, W: 758, H: 32},
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
