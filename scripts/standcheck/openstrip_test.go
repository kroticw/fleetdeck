package main

import (
	"encoding/json"
	"testing"
)

// openSurface is the unfolded orchestrator surface's word on its fit, its fleet
// menu's list open as fleet reports it: the page as wide as the surface, the
// list inside it.
func openSurface(t *testing.T, fleet string) stripReport {
	t.Helper()
	var s stripReport
	raw := `{"surface":"orchestrator","report":"overflow","folded":false,"width":313,"scrollWidth":313,"overflowing":[{"element":"span.o-name","scrollWidth":170,"clientWidth":67,"left":8,"right":75.4}` + fleet + `]}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAnUnfoldedOrchestratorSurfaceThatFitsIsNoProblem(t *testing.T) {
	s := openSurface(t, `,{"element":"div.fleet-menu-list","scrollWidth":224,"clientWidth":224,"left":81,"right":305}`)
	if got := check(logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), s), 0); len(got) != 0 {
		t.Fatalf("problems %q, want none: a name cut with an ellipsis and a list inside the surface", got)
	}
}

// Run 34949576998 (#185), the fleet menu open on the unfolded orchestrator
// island: its list ran from the button's left edge 224 px on, past the
// surface's right edge, and the page scrolled sideways, a scroll bar at the
// island's foot.
func TestAnUnfoldedOrchestratorSurfaceThatScrollsSidewaysIsAProblem(t *testing.T) {
	s := openSurface(t, `,{"element":"header","scrollWidth":426,"clientWidth":313,"left":0,"right":313},{"element":"div.fleet-menu-list","scrollWidth":224,"clientWidth":224,"left":201.9,"right":425.9}`)
	s.ScrollWidth = 426
	problems := check(logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), s), 0)
	wantProblem(t, problems, "orchestrator surface's page is 426 wide in 313", "scrolls sideways")
	wantProblem(t, problems, "fleet menu's list", "201.9..425.9", "313")
}

// The list anchored to a fleet button near the island's left edge, on a narrow
// island, would run past the other edge.
func TestAFleetMenuListPastTheSurfacesLeftEdgeIsAProblem(t *testing.T) {
	s := openSurface(t, `,{"element":"div.fleet-menu-list","scrollWidth":224,"clientWidth":224,"left":-12,"right":212}`)
	wantProblem(t, check(logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), s), 0), "fleet menu's list", "-12..212")
}

// The gate holds in full screen too, where the header is wider.
func TestAnUnfoldedOrchestratorSurfaceThatScrollsSidewaysInFullScreenIsAProblem(t *testing.T) {
	s := openSurface(t, "")
	s.ScrollWidth = 400
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), goodFrame(true), goodBoard(true), s, goodFrame(false), goodBoard(false))
	wantProblem(t, check(log, 1), "in full screen", "scrolls sideways")
}
