package state

import (
	"fmt"
	"sort"
	"time"
)

// Event is a change worth a banner (spec section 6).
type Event struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// sessionRuleOrder is the order the three session rules are reported in, so that a
// session standing on more than one produces the same list of keys every run.
var sessionRuleOrder = [...]string{"waiting", "failed", "silent"}

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
		rules["waiting"] = true
	}
	if s.State == "failed" {
		rules["failed"] = true
	}
	// Note that being Stalled() is no exemption here. Stalled has no banner of its
	// own because it resolves itself — but spec section 1's recorded case is three
	// sessions standing for two hours after the limit that stalled them had already
	// reset. When a stall does not resolve, this rule is the only one left that
	// calls a person.
	if silenceAfter > 0 && s.SilentFor >= silenceAfter {
		rules["silent"] = true
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

// sessionRuleText is the banner body for a rule that has just become true.
func sessionRuleText(rule string, silenceAfter time.Duration) string {
	switch rule {
	case "waiting":
		return "is waiting for an answer"
	case "failed":
		return "ended in failure"
	case "silent":
		return fmt.Sprintf("has been silent for over %s", silenceAfter)
	}
	return ""
}

// Diff compares two snapshots and returns the banners to fire and the keys to
// clear. An event is a transition, never a standing state: a rule fires when
// its condition becomes true and stays quiet for as long as it remains true,
// per spec section 6 ("повторные баннеры по одному и тому же событию не
// шлются: событием считается смена состояния, а не его наличие"). The very
// very first snapshot — prev was never assembled, so its At is zero — fires
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
				fire = append(fire, Event{Key: key, Title: s.Name, Text: sessionRuleText(rule, silenceAfter)})
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

	notifiableStage := func(stage string) bool { return stage == "blocked" || stage == "review" }

	prevStage := map[string]string{}
	for _, c := range prev.Cards {
		prevStage[c.Path] = c.Stage
	}
	for _, c := range next.Cards {
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
				Title: c.Title,
				Text:  "card moved to " + c.Stage,
			})
		}
		if notifiableStage(was) {
			cleared = append(cleared, fmt.Sprintf("card:%s:%s", c.Path, was))
		}
	}
	return fire, cleared
}
