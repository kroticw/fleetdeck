package main

import (
	"encoding/json"
	"testing"
)

// The operator on v0.10.2's dev build asked for the panels' unfold control as a
// round glass button; it is the only way back to a folded panel, and its target
// is held to at least 36 pt each way, as round as it is wide. Run 34949576998
// measured the bordered one at 26.2 by 40.
func TestAnUnfoldControlSmallerThanItsRoundTargetIsAProblem(t *testing.T) {
	for _, c := range []struct {
		surface, unfold, want string
	}{
		{"orchestrator", `{"unfold":{"left":10.9,"top":80,"right":37.1,"bottom":120,"reachable":true}}`, "26.2 by 40"},
		{"sessions", `{"unfold":{"left":4,"top":8,"right":44,"bottom":34,"reachable":true}}`, "40 by 26"},
	} {
		s := goodStrip()
		f := foldedOrchestrator(goodFrame(false), 89)
		board := besideFoldedStrip(goodBoard(false))
		if c.surface == "sessions" {
			s = goodSessionsStrip()
			f = foldedSessions(goodFrame(false))
			board = goodBoard(false)
		}
		if err := json.Unmarshal([]byte(c.unfold), &s); err != nil {
			t.Fatal(err)
		}
		wantProblem(t, check(logOf(t, f, board, goodHeader(false), s), 0), c.surface, "unfold control", c.want, "36")
	}
}
