package main

import (
	"errors"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/state"
)

// recorder stands in for *notify.Notifier: the delivery path is what is under test,
// not macOS.
type recorder struct {
	fired   []string
	cleared []string
	err     error
}

func (r *recorder) Fire(key, _, _ string) error {
	r.fired = append(r.fired, key)
	return r.err
}

func (r *recorder) Clear(key string) { r.cleared = append(r.cleared, key) }

// allRules is one event of each of the four kinds state.Event.Kind can hold, which
// are one-to-one with the four booleans under notify.enabled.
func allRules() []state.Event {
	return []state.Event{
		{Key: "session:a:waiting", Kind: "waiting", Short: "a"},
		{Key: "session:b:failed", Kind: "failed", Short: "b"},
		{Key: "session:c:silent", Kind: "silent", Short: "c"},
		{Key: "card:d.md:blocked", Kind: "card_blocked", Path: "d.md"},
	}
}

func contains(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// TestEachToggleSilencesOnlyItsOwnRule is the whole point of the four settings: spec
// section 6 says every rule can be switched off, and a setting that is parsed but
// never read is worse than a missing one — the operator turns silence banners off,
// keeps getting them, and concludes the panel is broken.
func TestEachToggleSilencesOnlyItsOwnRule(t *testing.T) {
	cases := []struct {
		name    string
		disable func(*config.NotifyConfig)
		silent  string
	}{
		{"waiting", func(n *config.NotifyConfig) { n.Waiting = false }, "session:a:waiting"},
		{"failed", func(n *config.NotifyConfig) { n.Failed = false }, "session:b:failed"},
		{"silent", func(n *config.NotifyConfig) { n.Silent = false }, "session:c:silent"},
		{"card_blocked", func(n *config.NotifyConfig) { n.CardBlocked = false }, "card:d.md:blocked"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default().Notify
			tc.disable(&cfg)

			rec := &recorder{}
			deliver(rec, cfg, allRules(), nil, func(error) {})

			if contains(rec.fired, tc.silent) {
				t.Fatalf("%s is switched off and must not fire, got %v", tc.name, rec.fired)
			}
			if len(rec.fired) != 3 {
				t.Fatalf("the other three rules must still fire, got %v", rec.fired)
			}
		})
	}
}

func TestEveryRuleFiresWhenNoneIsDisabled(t *testing.T) {
	rec := &recorder{}
	deliver(rec, config.Default().Notify, allRules(), nil, func(error) {})

	if len(rec.fired) != 4 {
		t.Fatalf("the defaults switch nothing off, want 4 banners, got %v", rec.fired)
	}
}

// TestClearsAreNotFiltered pins that a disabled rule does not also disable letting go
// of keys. Clearing is how the notifier forgets a standing state, and a key held for
// a rule that was on and has since been switched off would otherwise be held forever.
func TestClearsAreNotFiltered(t *testing.T) {
	cfg := config.Default().Notify
	cfg.Waiting = false

	rec := &recorder{}
	deliver(rec, cfg, nil, []string{"session:a:waiting", "card:d.md:review"}, func(error) {})

	if len(rec.cleared) != 2 {
		t.Fatalf("every key must be released regardless of toggles, got %v", rec.cleared)
	}
}

// TestAnUnownedKindIsNotFired pins what happens to a fifth rule added to
// internal/state with no toggle of its own: it does not fire. A rule nobody can
// switch off would break the promise the other four keep, and a new rule that stays
// quiet is caught by whoever adds it, in their own tests.
func TestAnUnownedKindIsNotFired(t *testing.T) {
	rec := &recorder{}
	deliver(rec, config.Default().Notify, []state.Event{{Key: "session:a:stalled", Kind: "stalled"}}, nil, func(error) {})

	if len(rec.fired) != 0 {
		t.Fatalf("a kind no toggle owns must not fire, got %v", rec.fired)
	}
}

// TestADeliveryFailureIsReportedAndDoesNotStopTheRest covers osascript failing on one
// banner: the remaining banners are still delivered.
func TestADeliveryFailureIsReportedAndDoesNotStopTheRest(t *testing.T) {
	rec := &recorder{err: errors.New("osascript exploded")}
	var reported int
	deliver(rec, config.Default().Notify, allRules(), nil, func(error) { reported++ })

	if len(rec.fired) != 4 {
		t.Fatalf("a failed banner must not swallow the others, got %v", rec.fired)
	}
	if reported != 4 {
		t.Fatalf("every failure must be reported, got %d", reported)
	}
}
