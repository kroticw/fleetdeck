package main

import (
	"path/filepath"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
)

// tallColumn is the fewest cards a column needs to be taller than the stand's
// window: 700 points, less the capsules' room, is under ten cards of two
// wrapped title lines.
const tallColumn = 20

// The operator's board had a column longer than the window, and on v0.10.1 the
// whole board scrolled down beside the sessions glass, with a classic bar at
// that glass's edge. The stand's board, one card a stage, never did (T-067).
func TestTheStandsBoardHasAColumnTallerThanTheStandsWindow(t *testing.T) {
	home, boardDir := t.TempDir(), t.TempDir()
	if err := layout(home, boardDir); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(boardDir, "cards", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	perStage := map[string]int{}
	for _, f := range files {
		c, err := board.ParseCard(f)
		if err != nil || c.ParseError != "" {
			t.Fatalf("%s: %v, %q", f, err, c.ParseError)
		}
		perStage[c.Stage]++
	}
	tallest := 0
	for _, n := range perStage {
		tallest = max(tallest, n)
	}
	if tallest < tallColumn {
		t.Fatalf("the tallest column holds %d cards, want at least %d: %v", tallest, tallColumn, perStage)
	}
}
