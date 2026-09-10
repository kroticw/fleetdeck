// Package state holds the snapshot type the panel is rendered from and the rules
// deciding which changes between two snapshots are worth a notification.
//
// It does not assemble the snapshot. Every source that feeds one — the daemon's
// session list, the board's cards, the transcript reader's context estimate, the
// usage fetcher's limits — reaches it as an argument, because this package performs
// no I/O whatsoever: that is what makes both the linking and the whole notification
// rule set testable from plain values with no daemon, no board directory and no
// network. The caller does the reading, fills in the fields Link cannot, and
// decides how often to look. This package knows nothing about the web server built
// on top of it and never writes anywhere.
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

	// Model and CostUSD come from the statusline reporter and from nowhere else.
	// Claude Code hands the model display name and the session's running cost to
	// its statusline command and to nothing outside the session (spec section
	// 3.2), which is the whole reason cmd/fleetdeck-status exists. Unlike Context,
	// neither has a transcript fallback: a session whose reporter is not installed
	// simply has no model name and no cost, and the panel must show that rather
	// than a number derived from something else.
	Model string `json:"model,omitempty"`

	// CostUSD is a pointer because "no report" and "a session that has so far cost
	// nothing" are different facts, and a plain float cannot tell them apart —
	// zero is a real cost a session genuinely can have. The same distinction the
	// zero SilentFor carries below, made explicit in the type instead of by
	// convention, because there is no rule here that reads a zero as absence.
	CostUSD *float64 `json:"costUSD,omitempty"`

	// Label is the operator's own name for this session, keyed by its
	// transcript UUID (Session.SessionID, never Short — see
	// internal/config.Config.SessionLabels' own comment for why) and copied
	// in by the caller (cmd/fleetdeck's Collector) from configuration. Empty
	// means the operator never named this session; the caller must not
	// invent one — falling back to Short, or to whatever daemon-supplied
	// name exists, is the frontend's job, not this package's.
	//
	// No frontend reads this field yet. It is stored and served starting
	// with this change; the panel starts rendering it in a following
	// change, once internal/server's session-label write route also has a
	// browser-side caller to pair with the neighbouring frontend restructure
	// this change deliberately does not touch.
	Label string `json:"label,omitempty"`
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

	// OrchestratorSession is the short session id pinned to the orchestrator
	// column, copied from configuration by the caller (cmd/fleetdeck's
	// Collector). Empty means nothing is pinned; the panel then offers a picker.
	OrchestratorSession string `json:"orchestratorSession,omitempty"`
}

// Link attaches each session to the card that names it. A card names a
// session through its Session field, which holds the session's short id
// (daemon.Session.Short) — never the transcript UUID (SessionID), and never
// something guessed from a title or a path. A session with no card naming it
// gets an empty CardPath; it must never borrow another session's card. A session
// whose own Short is empty is linked to nothing: an empty short id is not an
// identity, so nothing can legitimately name it. It still gets a view — a session
// the panel cannot link is still a session the panel must show.
//
// Two cards naming the same session is a mistake on the board, but one this
// function still has to resolve, and the answer must not depend on the order
// board.Scan happens to return: the lexicographically smallest Path wins. That is
// an arbitrary rule chosen for being stable — the same board produces the same
// link on every tick, and a neighbour package changing its sort cannot silently
// move a card from one session to another.
//
// Link fills Session and CardPath and nothing else. Context, SilentFor, Model and
// CostUSD are the caller's to fill: the first two require I/O (reading the session's
// transcript from disk) and the last two arrive from the statusline reporter,
// and this package does neither — Task 11's Collect does both and hands the
// finished views back. So a zero SilentFor out of Link means "not measured", never
// "not silent", and the two cannot be told apart from the field alone. Silence is
// measured from the transcript, as the age of the last write to the session's
// file, and a session may well have no transcript at all: one that has just
// started and whose file does not exist yet, one transcript.Locate cannot find,
// one from another backend. Anything reading SilentFor must therefore treat zero
// as "no measurement" — the silence rule in Diff does, which is why a session in
// its first second of life does not get told it has been silent for half an hour.
func Link(sessions []daemon.Session, cards []board.Card) []SessionView {
	cardByShort := map[string]string{}
	for _, c := range cards {
		if c.Session == "" {
			continue
		}
		if won, taken := cardByShort[c.Session]; taken && won <= c.Path {
			continue
		}
		cardByShort[c.Session] = c.Path
	}
	views := make([]SessionView, 0, len(sessions))
	for _, s := range sessions {
		path := ""
		if s.Short != "" {
			path = cardByShort[s.Short]
		}
		views = append(views, SessionView{Session: s, CardPath: path})
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
