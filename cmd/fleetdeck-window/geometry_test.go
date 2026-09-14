//go:build darwin

package main

import "testing"

func TestTheDefaultLayoutMatchesTheChosenDesign(t *testing.T) {
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})
	want := geometry{
		Orchestrator: rect{X: 8, Y: 8, W: 368, H: 966},
		Sessions:     rect{X: 1156, Y: 8, W: 348, H: 966},
		Capsules:     rect{X: 386, Y: 12, W: 758, H: 32},
		// Right is the sessions panel and its margin: the board still scrolls
		// under the glass, but a card, a session or a document opens clear of it.
		Board: insets{Top: 64, Left: 394, Right: 356},
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
	if g.Board.Right != 56 {
		t.Fatalf("board right inset = %v, want the folded strip and its margin", g.Board.Right)
	}
}

func TestAPanelNeverGoesBelowItsReadableWidthOrAboveSixtyPercent(t *testing.T) {
	width := 1512.0
	g := layoutFor(width, 982, panelWidths{Orchestrator: 100, Sessions: 5000})
	if g.Orchestrator.W != 220 {
		t.Fatalf("orchestrator = %v, want the 220 floor", g.Orchestrator.W)
	}
	// Multiplied at run time, as layoutFor does: the constant 1512*0.6 is exact
	// and the float product is not.
	if g.Sessions.W != width*maxPanelShare {
		t.Fatalf("sessions = %v, want 60%% of the window", g.Sessions.W)
	}
}
