// Package state assembles one snapshot out of the daemon's session list, the
// board's cards, the transcript reader's context estimate and the usage
// fetcher's limits, and decides which changes between two snapshots are
// worth a notification. It performs no I/O of its own, knows nothing about
// the web server built on top of it, and never writes anywhere.
package state

import (
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
)

// SessionView is a daemon session enriched with what the other two sources
// know about it.
type SessionView struct {
	daemon.Session
	Context   *transcript.Usage `json:"context,omitempty"`
	CardPath  string            `json:"cardPath,omitempty"`
	SilentFor time.Duration     `json:"silentFor"`
}

// Snapshot is everything the panel shows at one moment, assembled from
// whatever each of the three sources could produce. A source that failed
// fills its own error field and leaves the rest of the snapshot untouched —
// this struct is where the design's "degrade in parts" rule (spec section 7)
// becomes code: a Snapshot with DaemonError set can still carry a full Cards
// list, and one with BoardError set can still carry a full Sessions list.
type Snapshot struct {
	Sessions []SessionView `json:"sessions"`
	Cards    []board.Card  `json:"cards"`
	Limits   *usage.Limits `json:"limits,omitempty"`
	// OrphanCards holds the path of every card whose Session field names a
	// session the daemon no longer lists — spec section 7's "карточка ссылается
	// на мёртвую сессию", which the panel must surface rather than quietly leave
	// pointing at nothing. The list is the caller's to fill from OrphanCards();
	// this package computes it but never assembles a Snapshot itself.
	OrphanCards []string  `json:"orphanCards,omitempty"`
	DaemonError string    `json:"daemonError,omitempty"`
	BoardError  string    `json:"boardError,omitempty"`
	UsageError  string    `json:"usageError,omitempty"`
	At          time.Time `json:"at"`
}

// Link attaches each session to the card that names it. A card names a
// session through its Session field, which holds the session's short id
// (daemon.Session.Short) — never the transcript UUID (SessionID), and never
// something guessed from a title or a path. A session with no card naming it
// gets an empty CardPath; it must never borrow another session's card.
func Link(sessions []daemon.Session, cards []board.Card) []SessionView {
	cardByShort := map[string]string{}
	for _, c := range cards {
		if c.Session != "" {
			cardByShort[c.Session] = c.Path
		}
	}
	views := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		views = append(views, SessionView{Session: s, CardPath: cardByShort[s.Short]})
	}
	return views
}

// OrphanCards lists the path of every card whose Session field names a short
// id that is not among sessions. A card whose Session field is empty is not
// reported: it never claimed a session in the first place, which is a
// different fact from a claim that turned out to be dead — a check that
// folded the two together could no longer tell "looked and found nothing
// wrong" apart from "there was nothing to look at".
func OrphanCards(sessions []daemon.Session, cards []board.Card) []string {
	alive := map[string]bool{}
	for _, s := range sessions {
		alive[s.Short] = true
	}
	var out []string
	for _, c := range cards {
		if c.Session != "" && !alive[c.Session] {
			out = append(out, c.Path)
		}
	}
	return out
}
