package state

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Event is a change worth a banner (spec section 6).
type Event struct {
	// Key identifies the standing state this event is about, so a banner already
	// shown is not shown again and can be taken back down when the state ends.
	// Its format is part of the contract with the notifier: "session:<short>:<rule>"
	// and "card:<path>:<stage>".
	Key string `json:"key"`
	// Kind is exactly one of "waiting", "failed", "silent" and "card_blocked", the
	// four toggles under notify.enabled in internal/config. It exists so a caller
	// deciding whether a rule is switched on can read the answer instead of
	// re-deriving it from Key: a card event's Key ends in the stage, so the single
	// card_blocked toggle would otherwise have to be matched against two different
	// suffixes ("blocked" and "review"), which is exactly the kind of duplicated
	// rule this package has already been bitten by.
	Kind string `json:"kind"`
	// Short is the daemon short id for a session event, empty for a card event;
	// Path is the card path for a card event, empty for a session event. Exactly
	// one of the two is set, so the subject of a banner can be acted on (opened,
	// replied to) without taking Key apart.
	Short string `json:"short,omitempty"`
	Path  string `json:"path,omitempty"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// The four event kinds, one per notify.enabled toggle in internal/config.
const (
	kindWaiting     = "waiting"
	kindFailed      = "failed"
	kindSilent      = "silent"
	kindCardBlocked = "card_blocked"
)

// sessionRuleOrder is the order the three session rules are reported in, so that a
// session standing on more than one produces the same list of keys every run.
var sessionRuleOrder = [...]string{kindWaiting, kindFailed, kindSilent}

// standingRules reports which of the three session rules s currently satisfies. It is
// the single place each rule is stated, so fire and clear cannot drift apart: a key is
// raised when it enters this set and released when it leaves, including by the session
// disappearing altogether.
//
// A Dying session satisfies nothing. Waiting() and Stalled() already exclude one for a
// stated reason (see their doc comments in internal/daemon): the job is being killed or
// retired, so nobody has to act on it. The same holds for the other two rules — the
// operator who kills a session should not then be told it "ended in failure" and, half
// an hour later, that it "has been silent". Clears are unaffected: a session that was
// waiting and is now dying has an empty rule set, so its waiting key is released.
func standingRules(s SessionView, silenceAfter time.Duration) map[string]bool {
	rules := map[string]bool{}
	if s.Dying {
		return rules
	}
	if s.Waiting() {
		rules[kindWaiting] = true
	}
	if s.State == "failed" {
		rules[kindFailed] = true
	}
	// Note that being Stalled() is no exemption here. Stalled has no banner of its
	// own because it resolves itself — but spec section 1's recorded case is three
	// sessions standing for two hours after the limit that stalled them had already
	// reset. When a stall does not resolve, this rule is the only one left that
	// calls a person.
	if silenceAfter > 0 && s.SilentFor >= silenceAfter {
		rules[kindSilent] = true
	}
	return rules
}

// sessionRuleSets indexes sessions by short id. A session with an empty Short is left
// out entirely: every key is built from the short id, so such a session can only
// produce "session::waiting" — a key that names nobody and that a second short-less
// session would collide with, silencing one of the two.
func sessionRuleSets(sessions []SessionView, silenceAfter time.Duration) map[string]map[string]bool {
	sets := map[string]map[string]bool{}
	for _, s := range sessions {
		if s.Short == "" {
			continue
		}
		sets[s.Short] = standingRules(s, silenceAfter)
	}
	return sets
}

// humanDuration renders a duration the way a person writes one — "30m", "1h 30m",
// "2h", "45s" — rather than the way Go prints one, which turns a plain half hour into
// "30m0s". Zero components are dropped rather than printed, since a banner is read at
// a glance. Anything under a second has no whole unit to render and is left to Go's
// own formatting; it never reaches a banner in practice, as such a threshold would
// have turned the silence rule off long before (see Diff).
func humanDuration(d time.Duration) string {
	if d < time.Second {
		return d.String()
	}
	d = d.Round(time.Second)
	parts := make([]string, 0, 3)
	if h := int(d / time.Hour); h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m := int(d % time.Hour / time.Minute); m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if sec := int(d % time.Minute / time.Second); sec > 0 {
		parts = append(parts, fmt.Sprintf("%ds", sec))
	}
	return strings.Join(parts, " ")
}

// sessionRuleText is the banner body for a rule that has just become true.
func sessionRuleText(rule string, silenceAfter time.Duration) string {
	switch rule {
	case kindWaiting:
		return "is waiting for an answer"
	case kindFailed:
		return "ended in failure"
	case kindSilent:
		return fmt.Sprintf("has been silent for over %s", humanDuration(silenceAfter))
	}
	return ""
}

// Diff compares two snapshots and returns the banners to fire and the keys to
// clear. An event is a transition, never a standing state: a rule fires when
// its condition becomes true and stays quiet for as long as it remains true,
// per spec section 6 ("повторные баннеры по одному и тому же событию не
// шлются: событием считается смена состояния, а не его наличие"). The very
// first snapshot — prev was never assembled, so its At is zero — fires
// nothing: every standing state in next would otherwise look brand new.
//
// That guard keys on prev.At and nothing else. An observed empty fleet is a fact
// ("last tick there was nothing running"), the absence of an observation is not, and
// only the second may be quiet: a fleet that starts empty and gains a session already
// waiting is precisely the case spec section 1 exists for.
//
// The four rules implemented here are exactly the four the design spec lists
// in section 6, matched to the four toggles in internal/config's
// notify.enabled (waiting, failed, silent, card_blocked):
//
//  1. a session started Waiting() — a person must answer it now;
//  2. a session's State became "failed";
//  3. a session's SilentFor crossed silenceAfter — unless silenceAfter is zero
//     or negative, which turns this rule off entirely. Spec line 248 defines
//     notify.silence_after as the threshold a session must be silent for before
//     the silence counts as an event, and no session can be silent for less than
//     no time at all: a zero threshold read as "report every silence" would put a
//     banner on the whole fleet the moment it is first seen. Off is the only
//     reading that leaves the setting a way to say "do not call me about this";
//  4. a card's Stage became "blocked" or "review".
//
// Deliberately absent: a session becoming Stalled() fires nothing on its own.
// Stalled() and Waiting() are mutually exclusive (daemon.Session.Stalled's own
// doc comment), and the design's four notification rules do not include a
// fifth "stalled" rule — the spec gives stalled sessions a header counter
// (section 4's "M остановились"), not a banner, because a stalled session by
// definition needs no person to act: it recovers on its own (a usage limit
// resets, a rate limit expires) or needs attention outside this console
// entirely (a login refresh). Promoting it to a banner would teach the
// operator to treat "the API is rate-limited" the same as "someone is
// waiting on you", which is exactly the conflation section 5 of
// docs/protocol/daemon-control-socket.md warns against.
//
// The silence rule is the deliberate exception to that quiet: it applies to a
// stalled session exactly as it does to a running one. "Resolves itself" is a claim
// about the usual case, not a guarantee — spec section 1's recorded failure is three
// sessions standing for two hours after the usage limit that stalled them had already
// reset. A stall that outlives the silence threshold has stopped being the quiet kind
// of trouble, and the silence rule is then the only rule left that calls a person.
func Diff(prev, next Snapshot, silenceAfter time.Duration) (fire []Event, cleared []string) {
	if prev.At.IsZero() {
		return nil, nil
	}

	prevRules := sessionRuleSets(prev.Sessions, silenceAfter)
	nextRules := sessionRuleSets(next.Sessions, silenceAfter)

	for _, s := range next.Sessions {
		if s.Short == "" {
			continue
		}
		was, now := prevRules[s.Short], nextRules[s.Short]
		for _, rule := range sessionRuleOrder {
			key := fmt.Sprintf("session:%s:%s", s.Short, rule)
			switch {
			case now[rule] && !was[rule]:
				fire = append(fire, Event{
					Key:   key,
					Kind:  rule,
					Short: s.Short,
					Title: s.Name,
					Text:  sessionRuleText(rule, silenceAfter),
				})
			case was[rule] && !now[rule]:
				cleared = append(cleared, key)
			}
		}
	}

	// A session in prev and not in next has ended. Its rules did not become false
	// one by one — the session simply stopped being observable — but every key it
	// was standing on must be released all the same, or it stays raised in the
	// notifier forever and the next session to take that short id is swallowed as
	// "already reported". Sorted, so two runs over the same pair of snapshots
	// produce the same list.
	var ended []string
	for short := range prevRules {
		if _, alive := nextRules[short]; !alive {
			ended = append(ended, short)
		}
	}
	sort.Strings(ended)
	for _, short := range ended {
		for _, rule := range sessionRuleOrder {
			if prevRules[short][rule] {
				cleared = append(cleared, fmt.Sprintf("session:%s:%s", short, rule))
			}
		}
	}

	// A card board.Scan could not parse arrives as Card{Path, ParseError} with an
	// empty Stage, and an empty Stage is not a stage — it is the absence of a
	// reading. Diffing against it turns one edit into two banners: the half-written
	// file reads as "left blocked", and the finished write on the next tick reads as
	// "entered blocked". Such a card is therefore skipped on both sides, so the next
	// tick has no phantom empty stage to diff against either. It is still counted as
	// present below, because it has not been deleted — only, for the moment, read.
	prevStage := map[string]string{}
	for _, c := range prev.Cards {
		if c.ParseError != "" {
			continue
		}
		prevStage[c.Path] = c.Stage
	}
	inNext := map[string]bool{}
	for _, c := range next.Cards {
		inNext[c.Path] = true
	}

	for _, c := range next.Cards {
		if c.ParseError != "" {
			continue
		}
		was, existed := prevStage[c.Path]
		// A card this diff has never seen before has no prior stage to compare
		// against, so — unlike a session, whose mere appearance already
		// waiting is itself news — a brand-new card is never reported here:
		// there is no way to tell "just moved to blocked" apart from "created
		// directly in blocked", so it stays quiet rather than guess.
		if !existed || was == c.Stage {
			continue
		}

		if notifiableStage(c.Stage) {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("card:%s:%s", c.Path, c.Stage),
				Kind:  kindCardBlocked,
				Path:  c.Path,
				Title: c.Title,
				Text:  "card moved to " + c.Stage,
			})
		}
		if notifiableStage(was) {
			cleared = append(cleared, fmt.Sprintf("card:%s:%s", c.Path, was))
		}
	}

	// A card in prev and not in next was deleted or moved out of the board. Like an
	// ended session, it never left its stage by a move anyone can see, so its key has
	// to be released here or it stays raised forever.
	var deleted []string
	for path, stage := range prevStage {
		if !inNext[path] && notifiableStage(stage) {
			deleted = append(deleted, fmt.Sprintf("card:%s:%s", path, stage))
		}
	}
	sort.Strings(deleted)
	cleared = append(cleared, deleted...)

	return fire, cleared
}

// notifiableStage reports whether a stage is one of the two the spec's fourth rule
// names (section 6: "карточка ушла в blocked или review").
func notifiableStage(stage string) bool { return stage == "blocked" || stage == "review" }
