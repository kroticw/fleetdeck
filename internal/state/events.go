package state

import (
	"fmt"
	"time"
)

// Event is a change worth a banner (spec section 6).
type Event struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Text  string `json:"text"`
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

	prevSessions := map[string]SessionView{}
	for _, s := range prev.Sessions {
		prevSessions[s.Short] = s
	}
	for _, s := range next.Sessions {
		was, existed := prevSessions[s.Short]

		if s.Waiting() && (!existed || !was.Waiting()) {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("session:%s:waiting", s.Short),
				Title: s.Name,
				Text:  "is waiting for an answer",
			})
		}
		if !s.Waiting() && existed && was.Waiting() {
			cleared = append(cleared, fmt.Sprintf("session:%s:waiting", s.Short))
		}

		if s.State == "failed" && (!existed || was.State != "failed") {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("session:%s:failed", s.Short),
				Title: s.Name,
				Text:  "ended in failure",
			})
		}
		if s.State != "failed" && existed && was.State == "failed" {
			cleared = append(cleared, fmt.Sprintf("session:%s:failed", s.Short))
		}

		silenceEnabled := silenceAfter > 0
		crossed := silenceEnabled && s.SilentFor >= silenceAfter && (!existed || was.SilentFor < silenceAfter)
		if crossed {
			fire = append(fire, Event{
				Key:   fmt.Sprintf("session:%s:silent", s.Short),
				Title: s.Name,
				Text:  fmt.Sprintf("has been silent for over %s", silenceAfter),
			})
		}
		if silenceEnabled && s.SilentFor < silenceAfter && existed && was.SilentFor >= silenceAfter {
			cleared = append(cleared, fmt.Sprintf("session:%s:silent", s.Short))
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
