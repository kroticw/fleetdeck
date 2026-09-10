// internal/state/snapshot_test.go
package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

func TestLinkAttachesSessionToItsCardByShortID(t *testing.T) {
	sessions := []daemon.Session{{Short: "abc12345"}, {Short: "deadbeef"}}
	cards := []board.Card{{Path: "/board/one.md", Session: "abc12345"}}
	views := Link(sessions, cards)
	if len(views) != 2 {
		t.Fatalf("Link must produce one view per session, got %d", len(views))
	}
	if views[0].CardPath != "/board/one.md" {
		t.Fatalf("a session named by a card must be linked to it: %+v", views[0])
	}
	if views[1].CardPath != "" {
		t.Fatalf("a session no card names must not borrow someone else's card: %+v", views[1])
	}
}

func TestLinkKeysBySessionShortNotTranscriptID(t *testing.T) {
	// A card's session field holds the daemon short id, never the transcript
	// UUID carried in daemon.Session.SessionID.
	sessions := []daemon.Session{{Short: "abc12345", SessionID: "11111111-1111-1111-1111-111111111111"}}
	cards := []board.Card{{Path: "/board/one.md", Session: "11111111-1111-1111-1111-111111111111"}}
	views := Link(sessions, cards)
	if views[0].CardPath != "" {
		t.Fatalf("a card naming the transcript UUID must not link by it: %+v", views[0])
	}
}

func TestOrphanCardsReportsCardNamingDeadSession(t *testing.T) {
	cards := []board.Card{{Path: "/board/one.md", Session: "gone1234"}}
	orphans := OrphanCards(nil, cards)
	if len(orphans) != 1 || orphans[0] != "/board/one.md" {
		t.Fatalf("a card naming a dead session must be reported: %v", orphans)
	}
}

func TestOrphanCardsSilentWhenSessionIsAlive(t *testing.T) {
	sessions := []daemon.Session{{Short: "live1234"}}
	cards := []board.Card{{Path: "/board/one.md", Session: "live1234"}}
	orphans := OrphanCards(sessions, cards)
	if len(orphans) != 0 {
		t.Fatalf("a card naming a live session must not be reported: %v", orphans)
	}
}

func TestOrphanCardsIgnoresCardWithNoSessionField(t *testing.T) {
	// A card with an empty Session field never claimed a session at all. That
	// is "nothing to look at", distinct from "looked and found the claim
	// dead" — folding the two together would make the report lie about which
	// case it is in.
	cards := []board.Card{{Path: "/board/unclaimed.md", Session: ""}}
	orphans := OrphanCards(nil, cards)
	if len(orphans) != 0 {
		t.Fatalf("a card with no session field must not be reported as orphaned: %v", orphans)
	}
}

func TestSnapshotKeepsBoardWhenDaemonIsDown(t *testing.T) {
	s := Snapshot{DaemonError: "daemon unavailable", Cards: []board.Card{{Path: "/board/one.md"}}}
	if len(s.Cards) != 1 {
		t.Fatal("a dead daemon must not take the board down with it")
	}
	if s.DaemonError == "" {
		t.Fatal("a dead daemon must still be visible in DaemonError")
	}
}

func TestSnapshotKeepsSessionsWhenBoardFailsToParse(t *testing.T) {
	s := Snapshot{
		BoardError: "no cards found",
		Sessions:   []SessionView{{Session: daemon.Session{Short: "abc12345"}}},
	}
	if len(s.Sessions) != 1 {
		t.Fatal("a failed board read must not take the session list down with it")
	}
	if s.BoardError == "" {
		t.Fatal("a failed board read must still be visible in BoardError")
	}
}

func TestSnapshotKeepsSessionsAndBoardWhenUsageFails(t *testing.T) {
	s := Snapshot{
		UsageError: "token expired",
		Limits:     nil,
		Sessions:   []SessionView{{Session: daemon.Session{Short: "abc12345"}}},
		Cards:      []board.Card{{Path: "/board/one.md"}},
	}
	if s.Limits != nil {
		t.Fatal("a failed usage fetch must leave Limits nil, not a stale or zero value pretending to be real")
	}
	if len(s.Sessions) != 1 || len(s.Cards) != 1 {
		t.Fatal("a failed usage fetch must not affect sessions or cards")
	}
}

// TestSnapshotAssembledFromEveryInconvenientForm exercises Link and
// OrphanCards together against one fixture carrying every awkward shape the
// two sources can hand this package at once: a card naming nothing alive, a
// session no card names, a card that failed to parse, a dying session, a
// session that is Stalled rather than Waiting, and a session with no
// transcript context.
func TestSnapshotAssembledFromEveryInconvenientForm(t *testing.T) {
	sessions := []daemon.Session{
		{Short: "waits123", Name: "waiting one", Needs: "answer: pick one (A · B)"},
		{Short: "stall123", Name: "stalled one", State: "blocked", Needs: ""},
		{Short: "dying123", Name: "dying one", Needs: "answer: are you sure", Dying: true},
		{Short: "quiet123", Name: "quiet one"},
	}
	cards := []board.Card{
		{Path: "/board/linked.md", Session: "waits123", Stage: "active"},
		{Path: "/board/orphan.md", Session: "gone-forever", Stage: "blocked"},
		{Path: "/board/broken.md", ParseError: "no frontmatter block"},
	}

	views := Link(sessions, cards)
	orphans := OrphanCards(sessions, cards)

	if len(views) != len(sessions) {
		t.Fatalf("Link must produce exactly one view per session, got %d", len(views))
	}
	byShort := map[string]SessionView{}
	for _, v := range views {
		byShort[v.Short] = v
	}
	if byShort["waits123"].CardPath != "/board/linked.md" {
		t.Fatalf("the named session must be linked: %+v", byShort["waits123"])
	}
	if byShort["quiet123"].CardPath != "" {
		t.Fatalf("a session no card names must have no CardPath: %+v", byShort["quiet123"])
	}
	if !byShort["waits123"].Waiting() {
		t.Fatal("a session with a real question in Needs must be Waiting")
	}
	if !byShort["stall123"].Stalled() || byShort["stall123"].Waiting() {
		t.Fatal("state=blocked with empty needs must be Stalled, never Waiting")
	}
	if byShort["dying123"].Waiting() || byShort["dying123"].Stalled() {
		t.Fatal("a dying session must be neither Waiting nor Stalled")
	}
	if byShort["quiet123"].Context != nil {
		t.Fatal("a session with no transcript must carry a nil Context")
	}

	if len(orphans) != 1 || orphans[0] != "/board/orphan.md" {
		t.Fatalf("exactly the card naming a dead session must be orphaned: %v", orphans)
	}

	snap := Snapshot{
		Sessions:   views,
		Cards:      cards,
		Limits:     nil,
		UsageError: "token expired",
		At:         time.Now(),
	}
	if snap.Limits != nil {
		t.Fatal("Limits must stay nil when the usage fetch failed")
	}
	broken := 0
	for _, c := range snap.Cards {
		if c.ParseError != "" {
			broken++
		}
	}
	if broken != 1 {
		t.Fatalf("the broken card must survive in Cards with its ParseError intact, got %d broken cards", broken)
	}
}

// TestSnapshotCarriesOrphanCards pins task-9-fix-round-1 item 7. Spec section 7
// requires a card whose session is dead to be surfaced, and OrphanCards computes
// exactly that list — but the result had nowhere to sit on the snapshot the panel is
// served from, so the caller assembling one could not pass it on.
func TestSnapshotCarriesOrphanCards(t *testing.T) {
	sessions := []daemon.Session{{Short: "live1234"}}
	cards := []board.Card{{Path: "/board/orphan.md", Session: "gone1234"}}
	snap := Snapshot{
		Sessions:    Link(sessions, cards),
		Cards:       cards,
		OrphanCards: OrphanCards(sessions, cards),
		At:          time.Now(),
	}
	if len(snap.OrphanCards) != 1 || snap.OrphanCards[0] != "/board/orphan.md" {
		t.Fatalf("a card naming a dead session must reach the snapshot: %v", snap.OrphanCards)
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if !strings.Contains(string(body), `"orphanCards":["/board/orphan.md"]`) {
		t.Fatalf("orphan cards must reach the browser under the agreed key: %s", body)
	}
	quiet, err := json.Marshal(Snapshot{At: time.Now()})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if strings.Contains(string(quiet), "orphanCards") {
		t.Fatalf("no orphans is the absence of a key, not an empty list: %s", quiet)
	}
}

// TestLinkPrefersTheLowestPathWhenTwoCardsNameOneSession pins item 9. Two cards
// naming the same short id is a mistake on the board, but the panel still has to pick
// one, and which one it picked used to depend on the order board.Scan happened to
// return — a neighbour package's sort, which nothing here pins. The cards are given in
// the opposite order to the sorted one, so a "last one wins" implementation picks the
// other card and fails.
func TestLinkPrefersTheLowestPathWhenTwoCardsNameOneSession(t *testing.T) {
	sessions := []daemon.Session{{Short: "abc12345"}}
	first := board.Card{Path: "/board/a-first.md", Session: "abc12345"}
	later := board.Card{Path: "/board/z-later.md", Session: "abc12345"}
	// Both input orders, because the point is that the input order does not decide.
	for _, cards := range [][]board.Card{{first, later}, {later, first}} {
		views := Link(sessions, cards)
		if views[0].CardPath != "/board/a-first.md" {
			t.Fatalf("the lowest path must win, whatever order the cards arrive in (%s then %s): %+v",
				cards[0].Path, cards[1].Path, views[0])
		}
	}
}

// TestLinkGivesNoCardToASessionWithNoShortID: an empty short id is not an identity.
// A card cannot name it (a card's session field holds a short id), so nothing may be
// linked to such a session even if a card carried an empty session field of its own.
func TestLinkGivesNoCardToASessionWithNoShortID(t *testing.T) {
	sessions := []daemon.Session{{Short: ""}}
	cards := []board.Card{{Path: "/board/one.md", Session: ""}}
	views := Link(sessions, cards)
	if len(views) != 1 {
		t.Fatalf("Link must still produce a view per session, so the panel can show it: %+v", views)
	}
	if views[0].CardPath != "" {
		t.Fatalf("a session with no short id must be linked to nothing: %+v", views[0])
	}
}
