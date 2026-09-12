package state

import (
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
)

// twoFleetCycle is one collect cycle over two fleets, with a card of B in
// stage and three sessions: A's, B's card session, and one nobody claims.
func twoFleetCycle(at time.Time, stage string, waiting bool) Snapshot {
	needs := ""
	if waiting {
		needs = "answer: go on?"
	}
	bCards := []board.Card{{Path: "/b/board/cards/two.md", Session: "b0000002", Stage: stage, Title: "ship it"}}
	sessions := []daemon.Session{
		{Short: "a0000001", Name: "A's orchestrator", Needs: daemon.Says(needs)},
		{Short: "b0000002", Name: "B's task", Needs: daemon.Says(needs)},
		{Short: "n0000001", Name: "nobody's", Needs: daemon.Says(needs)},
	}
	return Snapshot{
		At:       at,
		Cards:    bCards,
		Sessions: Link(sessions, bCards),
		Boards: []FleetBoard{
			{Fleet: fleet.Fleet{Name: "A", BoardPath: "/a/board", Orchestrator: "a0000001"}},
			{Fleet: fleet.Fleet{Name: "B", BoardPath: "/b/board"}, Cards: bCards},
		},
	}
}

func titles(events []Event) map[string]string {
	out := map[string]string{}
	for _, e := range events {
		out[e.Key] = e.Title
	}
	return out
}

func TestBannersNameTheFleetTheyComeFrom(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	prev := twoFleetCycle(t0, "active", false)
	next := twoFleetCycle(t0.Add(2*time.Second), "review", true)
	fire, _ := Diff(prev, next, 0)
	got := titles(fire)
	want := map[string]string{
		"card:/b/board/cards/two.md:review": "B: ship it",
		"session:a0000001:waiting":          "A: A's orchestrator",
		"session:b0000002:waiting":          "B: B's task",
		"session:n0000001:waiting":          "nobody's",
	}
	for key, title := range want {
		if got[key] != title {
			t.Errorf("%s titled %q, want %q", key, got[key], title)
		}
	}
	if len(got) != len(want) {
		t.Errorf("fired %v, want exactly %v", got, want)
	}
}

func TestOneFleetsBannersAreAsBefore(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC)
	prev := twoFleetCycle(t0, "active", false)
	next := twoFleetCycle(t0.Add(2*time.Second), "review", true)
	prev.Boards, next.Boards = prev.Boards[1:], next.Boards[1:]
	fire, _ := Diff(prev, next, 0)
	got := titles(fire)
	if got["card:/b/board/cards/two.md:review"] != "ship it" || got["session:b0000002:waiting"] != "B's task" {
		t.Fatalf("a one-fleet panel's banners gained a fleet name: %v", got)
	}
}
