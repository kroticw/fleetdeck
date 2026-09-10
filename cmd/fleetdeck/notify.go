package main

import (
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/state"
)

// banner is the part of *notify.Notifier the delivery path uses. It is an interface
// so the filtering below is testable without macOS, which is the only thing the real
// notifier can talk to.
type banner interface {
	Fire(key, title, text string) error
	Clear(key string)
}

// ruleEnabled reports whether the notify.enabled toggle that owns an event's kind is
// switched on.
//
// This is the single mapping from state.Event.Kind to the four booleans
// internal/config parses, and it is why Kind exists at all: without it a caller would
// have to re-derive the owning rule from Key, whose card form ends in the stage, so
// the one card_blocked toggle would need matching against two different suffixes.
//
// A kind no toggle owns does not fire. That case only arises if a fifth rule is added
// to internal/state without deciding which setting switches it off, and of the two
// possible defaults this is the safer one: a rule that fires with no way to turn it
// off breaks the promise spec section 6 makes about all four of these, while a rule
// that stays quiet fails visibly in the tests of whoever adds it.
func ruleEnabled(n config.NotifyConfig, kind string) bool {
	switch kind {
	case "waiting":
		return n.Waiting
	case "failed":
		return n.Failed
	case "silent":
		return n.Silent
	case "card_blocked":
		return n.CardBlocked
	default:
		return false
	}
}

// deliver hands what state.Diff produced to the notifier: the keys to release first,
// then the banners to raise, minus every banner whose rule the operator has switched
// off.
//
// Clears are deliberately not filtered. A key is how the notifier remembers it has
// already reported a standing state, and one raised while a rule was switched on
// would be held forever if switching the rule off also stopped the release — the
// session's next genuine transition would then pass in silence.
//
// A failed banner is reported through onError and does not stop the ones after it:
// osascript can fail for reasons that have nothing to do with the next banner, and
// the panel's own counters are the reliable channel either way.
func deliver(n banner, cfg config.NotifyConfig, fire []state.Event, cleared []string, onError func(error)) {
	for _, key := range cleared {
		n.Clear(key)
	}
	for _, e := range fire {
		if !ruleEnabled(cfg, e.Kind) {
			continue
		}
		if err := n.Fire(e.Key, e.Title, e.Text); err != nil {
			onError(err)
		}
	}
}
