package state

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/transcript"
)

// wholeTwoFleets is what a collect cycle produces for two fleets: every
// session the daemon lists, every card of both boards, and each board apart.
//
//	a0000001  fleet A's orchestrator
//	a0000002  on a card of A
//	b0000001  fleet B's orchestrator
//	b0000002  on a card of B
//	ab000001  on a card of A and a card of B
//	n0000001  on no card and no fleet's orchestrator
func wholeTwoFleets() Snapshot {
	fa := fleet.Fleet{Name: "A", BoardPath: "/a/board", Orchestrator: "a0000001"}
	fb := fleet.Fleet{Name: "B", BoardPath: "/b/board", Orchestrator: "b0000001"}
	aCards := []board.Card{
		{Path: "/a/board/cards/one.md", Session: "a0000002", Stage: "active"},
		{Path: "/a/board/cards/shared.md", Session: "ab000001", Stage: "active"},
		{Path: "/a/board/cards/dead.md", Session: "dead0001", Stage: "active"},
	}
	bCards := []board.Card{
		{Path: "/b/board/cards/two.md", Session: "b0000002", Stage: "review"},
		{Path: "/b/board/cards/shared.md", Session: "ab000001", Stage: "active"},
	}
	sessions := []daemon.Session{
		{Short: "a0000001", SessionID: "11111111-0000-0000-0000-000000000001"},
		{Short: "a0000002"},
		{Short: "b0000001"},
		{Short: "b0000002"},
		{Short: "ab000001"},
		{Short: "n0000001"},
	}
	all := append(append([]board.Card{}, aCards...), bCards...)
	views := Link(sessions, all)
	views[0].Label = "оркестр A"
	views[0].SilentFor = 3 * time.Minute
	views[0].Context = &transcript.Usage{Tokens: 40, Window: 100}
	views[0].Model = "Opus"
	return Snapshot{
		Sessions:    views,
		Cards:       all,
		At:          time.Date(2026, 9, 11, 20, 0, 0, 0, time.UTC),
		DaemonError: "",
		Boards: []FleetBoard{
			{Fleet: fa, Cards: aCards},
			{Fleet: fb, Cards: bCards, BoardError: "b is half read"},
		},
	}
}

func byShort(views []SessionView) map[string]SessionView {
	out := map[string]SessionView{}
	for _, v := range views {
		out[v.Short] = v
	}
	return out
}

func TestForFleetShowsThatFleetsBoardAndOrchestrator(t *testing.T) {
	whole := wholeTwoFleets()
	view, err := ForFleet(whole, "B")
	if err != nil {
		t.Fatalf("ForFleet(B): %v", err)
	}
	if view.Fleet != "B" || !reflect.DeepEqual(view.Fleets, []string{"A", "B"}) {
		t.Fatalf("fleet %q, fleets %v", view.Fleet, view.Fleets)
	}
	if !reflect.DeepEqual(view.Cards, whole.Boards[1].Cards) {
		t.Fatalf("B's view carries cards %v", view.Cards)
	}
	if view.OrchestratorSession != "b0000001" {
		t.Fatalf("B's view pins orchestrator %q", view.OrchestratorSession)
	}
	if view.BoardError != "b is half read" {
		t.Fatalf("B's view carries board error %q", view.BoardError)
	}
	if len(view.OrphanCards) != 0 {
		t.Fatalf("B has no card naming a dead session, got %v", view.OrphanCards)
	}
	if view.Boards != nil {
		t.Fatalf("a view must not carry every fleet's boards: %v", view.Boards)
	}
}

func TestForFleetKeepsEverySessionAndTagsItsFleets(t *testing.T) {
	// A session of another fleet is not hidden — it is shown apart, and a
	// session no fleet claims is shown in every fleet — so the view keeps them
	// all and says which fleets claim each.
	view, err := ForFleet(wholeTwoFleets(), "B")
	if err != nil {
		t.Fatalf("ForFleet(B): %v", err)
	}
	got := map[string][]string{}
	for _, v := range view.Sessions {
		got[v.Short] = v.Fleets
	}
	want := map[string][]string{
		"a0000001": {"A"},
		"a0000002": {"A"},
		"b0000001": {"B"},
		"b0000002": {"B"},
		"ab000001": {"A", "B"},
		"n0000001": nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("session fleets =\n%v\nwant\n%v", got, want)
	}
}

func TestForFleetLinksSessionsOnlyToThatFleetsCards(t *testing.T) {
	view, err := ForFleet(wholeTwoFleets(), "B")
	if err != nil {
		t.Fatalf("ForFleet(B): %v", err)
	}
	s := byShort(view.Sessions)
	cases := map[string]string{
		"a0000002": "", // its card is on A's board, which this tab does not show
		"b0000002": "/b/board/cards/two.md",
		"ab000001": "/b/board/cards/shared.md",
		"n0000001": "",
	}
	for short, want := range cases {
		if got := s[short].CardPath; got != want {
			t.Errorf("%s links to %q in B's view, want %q", short, got, want)
		}
	}
}

func TestForFleetReportsOrphansOfThatFleetOnly(t *testing.T) {
	view, err := ForFleet(wholeTwoFleets(), "A")
	if err != nil {
		t.Fatalf("ForFleet(A): %v", err)
	}
	if !reflect.DeepEqual(view.OrphanCards, []string{"/a/board/cards/dead.md"}) {
		t.Fatalf("A's orphans = %v", view.OrphanCards)
	}
	if view.BoardError != "" {
		t.Fatalf("A's view carries B's board error %q", view.BoardError)
	}
}

func TestForFleetKeepsWhatTheCycleMeasured(t *testing.T) {
	view, err := ForFleet(wholeTwoFleets(), "B")
	if err != nil {
		t.Fatalf("ForFleet(B): %v", err)
	}
	v := byShort(view.Sessions)["a0000001"]
	if v.Label != "оркестр A" || v.SilentFor != 3*time.Minute || v.Model != "Opus" || v.Context == nil || v.Context.Tokens != 40 {
		t.Fatalf("enrichment lost in another fleet's view: %+v", v)
	}
	if !view.At.Equal(wholeTwoFleets().At) {
		t.Fatalf("At changed: %v", view.At)
	}
}

func TestForFleetWithNoNameIsTheFirstFleet(t *testing.T) {
	view, err := ForFleet(wholeTwoFleets(), "")
	if err != nil {
		t.Fatalf("ForFleet(\"\"): %v", err)
	}
	if view.Fleet != "A" || view.OrchestratorSession != "a0000001" {
		t.Fatalf("an unnamed tab got fleet %q", view.Fleet)
	}
}

func TestForFleetRefusesAnUnknownFleet(t *testing.T) {
	_, err := ForFleet(wholeTwoFleets(), "C")
	if !errors.Is(err, fleet.ErrUnknown) {
		t.Fatalf("ForFleet(C) error = %v, want fleet.ErrUnknown", err)
	}
}

func TestForFleetLeavesTheWholeSnapshotAlone(t *testing.T) {
	// The whole snapshot is shared by every tab; one tab's view must not
	// relink or tag the sessions another tab is about to cut its view from.
	whole := wholeTwoFleets()
	before := byShort(whole.Sessions)["ab000001"].CardPath
	if _, err := ForFleet(whole, "B"); err != nil {
		t.Fatal(err)
	}
	after := byShort(whole.Sessions)["ab000001"]
	if after.CardPath != before || after.Fleets != nil {
		t.Fatalf("ForFleet changed the whole snapshot: %+v", after)
	}
}

func TestForFleetOfASnapshotWithNoBoardsIsThatSnapshot(t *testing.T) {
	// Before the first cycle there is nothing to cut: the zero snapshot is
	// served as it is, as it always was.
	zero := Snapshot{}
	view, err := ForFleet(zero, "")
	if err != nil {
		t.Fatalf("ForFleet(zero): %v", err)
	}
	if !reflect.DeepEqual(view, zero) {
		t.Fatalf("ForFleet(zero) = %+v", view)
	}
}
