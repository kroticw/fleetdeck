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
	"github.com/kroticw/fleetdeck/internal/buildinfo"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
)

// The two values Snapshot.LimitsSource takes -- see its own doc comment for
// why both the caller (cmd/fleetdeck's Collector) and the frontend need to
// know which one answered, not just how old the answer is.
const (
	LimitsSourceLocal   = "local"
	LimitsSourceNetwork = "network"
)

// The three states a session can be in, and the only values
// SessionView.Lifecycle takes.
//
// They are three and not two on purpose. "Not live" folds together a session
// that is paused and comes back with its whole history, and one that can
// never come back at all — and those are different things to a person and
// different things to act on: the first is work waiting to be picked up, the
// second is work that has to be started again.
//
// An empty Lifecycle reads as live. Nothing that builds a snapshot leaves it
// empty, but a view built by Link alone has not been told yet, and a session
// the daemon is listing is exactly what that is.
const (
	// LifecycleLive: the daemon's own list carries this session.
	LifecycleLive = "live"
	// LifecycleStopped: the daemon no longer lists it, but the job store
	// still has it and it can be resumed in place, with its full history.
	LifecycleStopped = "stopped"
	// LifecycleDead: the job store has it, and it cannot be brought back —
	// no id to resume by, or its working directory is gone. It is still
	// shown: a person has to be able to see that the work is there and that
	// it is not coming back, which is not the same as the row silently
	// disappearing.
	LifecycleDead = "dead"
)

// SessionView is a daemon session enriched with what the other two sources
// know about it.
type SessionView struct {
	daemon.Session
	Context   *transcript.Usage `json:"context,omitempty"`
	CardPath  string            `json:"cardPath,omitempty"`
	SilentFor time.Duration     `json:"silentFor"`

	// Lifecycle is which of the three states above this session is in. It is
	// derived, never read off the wire: the control protocol has no `live`
	// and no `resumable` field, and says so (docs/protocol/
	// daemon-control-socket.md section 4). The caller (cmd/fleetdeck's
	// Collector) decides it from the daemon's list and Claude Code's job
	// store together, which is the only place both are known.
	Lifecycle string `json:"lifecycle,omitempty"`

	// LastState is the state a session that is no longer running recorded
	// before it went away. Empty for a live session, which carries its state
	// in Session.State like it always did.
	//
	// It is a separate field, and not simply Session.State filled in from the
	// job store, because State is a vocabulary rules key on: "blocked" there
	// means stalled (daemon.Session.Stalled, and the same rule again in the
	// browser), and a session that stopped while blocked would go on being
	// counted as stalled every poll, forever, with nobody able to unstick it.
	// Keeping the frozen value out of the field those rules read makes that
	// impossible rather than something every future caller has to remember —
	// and the value is still shown, as what it is: the last state, not the
	// current one.
	LastState string `json:"lastState,omitempty"`

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

	// Fleets names the fleets claiming this session — the fleet it is the
	// orchestrator of, and every fleet whose board has a card naming it — in
	// configuration order. Empty means no fleet claims it. Filled by ForFleet;
	// see internal/fleet for why this is derived and never stored.
	Fleets []string `json:"fleets,omitempty"`
}

// Live reports whether the daemon is still running this session. An unset
// Lifecycle reads as live, per the constants' own doc comment.
func (v SessionView) Live() bool {
	return v.Lifecycle != LifecycleStopped && v.Lifecycle != LifecycleDead
}

// Resumable reports whether this session is stopped and can be brought back
// in place with its full history. False for a live session — there is
// nothing to resume — and false for a dead one, which is the whole point of
// telling those two apart.
func (v SessionView) Resumable() bool {
	return v.Lifecycle == LifecycleStopped
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
	// LimitsSource names which of the two places Limits came from: "local"
	// (a session's statusline already wrote the rate-limit windows to disk,
	// see internal/usage/localfile.go) or "network" (usage.Fetcher asked the
	// account endpoint itself, the fallback for a machine no statusline has
	// ticked on yet). Age alone cannot tell these apart -- a two-minute-old
	// local file and a two-minute-old network answer look identical on the
	// gauge, but mean different things: the first is normal (no session has
	// run recently), the second means the endpoint itself is degraded. Set
	// only alongside Limits; empty whenever Limits is nil.
	LimitsSource string `json:"limitsSource,omitempty"`
	// OrphanCards holds the path of every card whose Session field names a
	// session the daemon no longer lists — spec section 7's "карточка ссылается
	// на мёртвую сессию", which the panel must surface rather than quietly leave
	// pointing at nothing. The list is the caller's to fill from OrphanCards();
	// this package computes it but never assembles a Snapshot itself.
	OrphanCards []string `json:"orphanCards,omitempty"`
	// StoppedCards holds the path of every card whose session is stopped but
	// resumable — paused work, not lost work. Kept apart from OrphanCards
	// rather than folded into it: the board says something different about
	// each, and a card the panel calls orphaned while its session waits in
	// the job store is the lie this field exists to stop.
	StoppedCards []string `json:"stoppedCards,omitempty"`
	DaemonError  string   `json:"daemonError,omitempty"`
	BoardError   string   `json:"boardError,omitempty"`
	// JobsError is set when Claude Code's job store could not be read, which
	// is what stopped and dead sessions are known from. The live sessions in
	// this snapshot are unaffected and are still shown: a source that failed
	// fills its own field and leaves the rest alone (spec section 7). It
	// matters that the panel says so rather than showing an empty stopped
	// group, which looks exactly like a fleet where nothing is stopped.
	JobsError  string `json:"jobsError,omitempty"`
	UsageError string `json:"usageError,omitempty"`
	// UsageErrorKind classifies UsageError for the frontend's wording choice:
	// "auth" when sign-in would actually fix it (usage.ErrNoToken or
	// usage.ErrUnauthorized), "rate_limit" when it is the account's own
	// request budget (usage.ErrRateLimited), "other" for anything not
	// classified, "" when UsageError is empty. See cmd/fleetdeck/collect.go's
	// classifyUsageError -- the point of this field existing at all is that
	// "sign-in needed" must never be shown for a cause sign-in cannot fix.
	UsageErrorKind string    `json:"usageErrorKind,omitempty"`
	At             time.Time `json:"at"`

	// OrchestratorSession is the short session id pinned to the orchestrator
	// column, copied from configuration by the caller (cmd/fleetdeck's
	// Collector). Empty means nothing is pinned; the panel then offers a picker.
	OrchestratorSession string `json:"orchestratorSession,omitempty"`
	// OrchestratorBriefPath is where this fleet's orchestrator brief belongs
	// (internal/orchestrator.BriefPath), and OrchestratorBriefMissing says
	// that OrchestratorSession names a session while nothing is there.
	//
	// The two together are the standing form of one silent failure: the pin
	// means "this session has been given the fleet's working order", and it
	// can be set by a path that writes no working order at all -- the
	// orchestrator column's dropdown, which moves the pin and sends nothing.
	// The wizard cannot leave that state behind (it writes the brief as its
	// first step and stops there if it cannot), but the dropdown can, and so
	// can deleting the file afterwards.
	//
	// That is why this is read every cycle rather than checked once when an
	// appointment is made. A file written and later removed by hand is
	// indistinguishable on disk from one never written, and a check that only
	// ran at appointment time would report neither -- it would fix half the
	// failure while looking like the whole of it.
	//
	// Missing is never true with nothing pinned: an absent brief claims
	// nothing when no session is claimed to have been given one.
	//
	// OrchestratorBriefForeign says that OrchestratorSession names a session
	// while the file at that path was not written by fleetdeck
	// (internal/orchestrator.ReadBriefState). It is the quieter of the two: an
	// orchestrator notices a missing brief when it goes to read it, but reads
	// a foreign one and works by it. The wizard refuses to replace such a file
	// (orchestrator.ErrNotOurs); the dropdown pins beside it without asking.
	// Missing and Foreign are never both true, and Foreign is never true with
	// nothing pinned, for the same reason Missing is not.
	OrchestratorBriefPath    string `json:"orchestratorBriefPath,omitempty"`
	OrchestratorBriefMissing bool   `json:"orchestratorBriefMissing,omitempty"`
	OrchestratorBriefForeign bool   `json:"orchestratorBriefForeign,omitempty"`

	// Build describes the panel that produced this snapshot. A page open for
	// hours compares Build.Web with the fingerprint its own document arrived
	// with, and a mismatch means the panel was replaced under it. Stamped by
	// internal/server, not by the Collector: the collector knows the fleet,
	// the server knows what it is serving. Nil when the panel was wired
	// without one, which the page reads as "nothing to compare".
	Build *buildinfo.Fingerprint `json:"build,omitempty"`

	// Fleet is the fleet this snapshot shows and Fleets every configured
	// fleet, in configuration order. Both are set by ForFleet, which cuts the
	// view one browser tab asked for out of the whole snapshot.
	Fleet  string   `json:"fleet,omitempty"`
	Fleets []string `json:"fleets,omitempty"`

	// Boards is every fleet's board as this cycle read it. Only the whole
	// snapshot a collect cycle produces carries it; Cards then holds the cards
	// of every board together, which is what the notification rules diff, so a
	// card moving to review raises its banner whichever fleet a tab shows.
	// Never served: a view carries its own fleet's cards in Cards.
	Boards []FleetBoard `json:"-"`
}

// FleetBoard is one fleet's board as a collect cycle read it.
type FleetBoard struct {
	Fleet      fleet.Fleet
	Cards      []board.Card
	BoardError string
	// BriefPath, BriefMissing and BriefForeign are this fleet's own answer to
	// the snapshot fields of the same names, filled per fleet because each
	// fleet has its own orchestrator and its own documentation directories,
	// and so its own brief in its own place.
	BriefPath    string
	BriefMissing bool
	BriefForeign bool
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

// OrphanCards lists the path of every card whose Session field names a
// session that is not coming back: one no session in the list has, or one
// whose session is dead (LifecycleDead). A card whose Session field is empty
// is not reported: it never claimed a session in the first place, which is a
// different fact from a claim that turned out to be dead — a check that
// folded the two together could no longer tell "looked and found nothing
// wrong" apart from "there was nothing to look at".
//
// A card whose session is merely stopped is NOT an orphan, and this is the
// half of the fix that shows up on the board. "Card lost its session" is
// what the panel said about every card whose agent had been stopped, while
// the session was sitting in the job store with its whole history, waiting
// to be resumed. StoppedCards reports those instead, as what they are.
func OrphanCards(sessions []SessionView, cards []board.Card) []string {
	return cardsWhere(sessions, cards, func(v SessionView, found bool) bool {
		return !found || v.Lifecycle == LifecycleDead
	})
}

// StoppedCards lists the path of every card whose session is stopped but
// resumable. The board marks these apart from both a working card and an
// orphaned one: the work is paused, not lost.
func StoppedCards(sessions []SessionView, cards []board.Card) []string {
	return cardsWhere(sessions, cards, func(v SessionView, found bool) bool {
		return found && v.Resumable()
	})
}

// cardsWhere reports the cards whose named session satisfies want. found is
// false when no session in the list carries the card's short id at all,
// which is a state no SessionView value can stand for.
func cardsWhere(sessions []SessionView, cards []board.Card, want func(v SessionView, found bool) bool) []string {
	byShort := make(map[string]SessionView, len(sessions))
	for _, s := range sessions {
		byShort[s.Short] = s
	}
	var out []string
	for _, c := range cards {
		if c.Session == "" {
			continue
		}
		v, found := byShort[c.Session]
		if want(v, found) {
			out = append(out, c.Path)
		}
	}
	return out
}
