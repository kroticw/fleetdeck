// internal/state/events_test.go
package state

import (
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

// observedAt stamps a prev snapshot as a real prior observation. Diff tells "no
// snapshot yet" from "an empty fleet" by prev.At alone (see its doc comment), so a
// prev built in a test without At set is the first-tick case, not the case these
// tests mean to exercise.
var observedAt = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func waitingView(short string) SessionView {
	return SessionView{Session: daemon.Session{Short: short, Name: "session " + short, Needs: "answer: pick one (A · B)"}}
}

func idleView(short string) SessionView {
	return SessionView{Session: daemon.Session{Short: short, Name: "session " + short}}
}

func stalledView(short string) SessionView {
	return SessionView{Session: daemon.Session{Short: short, Name: "session " + short, State: "blocked", Detail: "waiting on my own subagents"}}
}

func TestSessionBecomingWaitingFiresOnce(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{waitingView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:waiting" {
		t.Fatalf("a session that started waiting must fire once: %+v", fire)
	}
	if len(cleared) != 0 {
		t.Fatalf("a fresh transition into waiting must not also clear something: %v", cleared)
	}
}

func TestStillWaitingDoesNotFireAgain(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{Sessions: []SessionView{waitingView("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a standing state is not a new event: %+v", fire)
	}
}

func TestSessionThatStoppedWaitingIsCleared(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{Sessions: []SessionView{idleView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a session leaving waiting must not itself fire: %+v", fire)
	}
	if len(cleared) != 1 || cleared[0] != "session:a:waiting" {
		t.Fatalf("a resolved state must be cleared so it can fire again: %v", cleared)
	}
}

// TestSessionBecomingStalledDoesNotFire records the deliberate design
// decision: Stalled() has no notification rule of its own (see the doc
// comment on Diff). A session moving straight from idle into Stalled
// (state=blocked, no words in Needs) must produce no banner at all.
func TestSessionBecomingStalledDoesNotFire(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{stalledView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("becoming Stalled must not fire any banner: %+v", fire)
	}
	if len(cleared) != 0 {
		t.Fatalf("there was no prior waiting state to clear: %v", cleared)
	}
}

// TestWaitingSessionThatBecomesStalledClearsWaitingButFiresNothingNew checks
// the boundary between the two: leaving Waiting still clears the waiting key
// (so it can fire again later), but arriving at Stalled fires nothing, since
// Stalled has no banner of its own.
func TestWaitingSessionThatBecomesStalledClearsWaitingButFiresNothingNew(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{Sessions: []SessionView{stalledView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("Stalled has no banner of its own, even right after Waiting cleared: %+v", fire)
	}
	if len(cleared) != 1 || cleared[0] != "session:a:waiting" {
		t.Fatalf("the resolved waiting state must still be cleared: %v", cleared)
	}
}

func TestSessionEndingInFailureFires(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	failed := idleView("a")
	failed.State = "failed"
	next := Snapshot{Sessions: []SessionView{failed}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:failed" {
		t.Fatalf("a session ending in failure must fire once: %+v", fire)
	}
}

func TestStillFailedDoesNotFireAgain(t *testing.T) {
	failed := idleView("a")
	failed.State = "failed"
	prev := Snapshot{At: observedAt, Sessions: []SessionView{failed}}
	next := Snapshot{Sessions: []SessionView{failed}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a standing failure is not a new event: %+v", fire)
	}
}

func TestFailedSessionThatRecoversIsCleared(t *testing.T) {
	failed := idleView("a")
	failed.State = "failed"
	prev := Snapshot{At: observedAt, Sessions: []SessionView{failed}}
	next := Snapshot{Sessions: []SessionView{idleView("a")}}
	_, cleared := Diff(prev, next, time.Hour)
	if len(cleared) != 1 || cleared[0] != "session:a:failed" {
		t.Fatalf("a session leaving the failed state must be cleared: %v", cleared)
	}
}

func TestSilenceFiresOnceOnCrossingTheThreshold(t *testing.T) {
	before := idleView("a")
	before.SilentFor = 10 * time.Minute
	after := idleView("a")
	after.SilentFor = 31 * time.Minute
	prev := Snapshot{At: observedAt, Sessions: []SessionView{before}}
	next := Snapshot{Sessions: []SessionView{after}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 1 || fire[0].Key != "session:a:silent" {
		t.Fatalf("crossing the silence threshold must fire: %+v", fire)
	}
}

func TestSilenceDoesNotFireAgainWhileStillOverThreshold(t *testing.T) {
	still := idleView("a")
	still.SilentFor = 45 * time.Minute
	prev := Snapshot{At: observedAt, Sessions: []SessionView{still}}
	next := Snapshot{Sessions: []SessionView{still}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 0 {
		t.Fatalf("staying silent past the threshold is a standing state, not a new event: %+v", fire)
	}
}

func TestSilenceClearsWhenActivityResumes(t *testing.T) {
	before := idleView("a")
	before.SilentFor = 45 * time.Minute
	after := idleView("a")
	after.SilentFor = 0
	prev := Snapshot{At: observedAt, Sessions: []SessionView{before}}
	next := Snapshot{Sessions: []SessionView{after}}
	_, cleared := Diff(prev, next, 30*time.Minute)
	if len(cleared) != 1 || cleared[0] != "session:a:silent" {
		t.Fatalf("resumed activity must clear the silence banner so it can fire again: %v", cleared)
	}
}

func TestCardEnteringBlockedFires(t *testing.T) {
	prev := Snapshot{At: observedAt, Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "blocked"}}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "card:/board/c.md:blocked" {
		t.Fatalf("a card entering blocked must fire: %+v", fire)
	}
}

func TestCardEnteringReviewFires(t *testing.T) {
	prev := Snapshot{At: observedAt, Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "review"}}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "card:/board/c.md:review" {
		t.Fatalf("a card entering review must fire: %+v", fire)
	}
}

func TestCardMovingBetweenNonNotifiableStagesFiresNothing(t *testing.T) {
	prev := Snapshot{At: observedAt, Cards: []board.Card{{Path: "/board/c.md", Stage: "new"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 || len(cleared) != 0 {
		t.Fatalf("new -> active is not one of the two notifiable stages: fire=%+v cleared=%v", fire, cleared)
	}
}

func TestCardLeavingBlockedIsCleared(t *testing.T) {
	prev := Snapshot{At: observedAt, Cards: []board.Card{{Path: "/board/c.md", Stage: "blocked"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	_, cleared := Diff(prev, next, time.Hour)
	if len(cleared) != 1 || cleared[0] != "card:/board/c.md:blocked" {
		t.Fatalf("a card leaving blocked must be cleared: %v", cleared)
	}
}

// TestCardBrandNewInBlockedDoesNotFire: a card this diff has never seen
// before has no prior stage to compare against. Unlike a session (see
// TestSessionBrandNewAlreadyWaitingDoesFire below), there is no way to tell
// "just moved to blocked" apart from "created directly in blocked" for a
// card, so a first sighting stays quiet rather than guess.
func TestCardBrandNewInBlockedDoesNotFire(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("z")}}
	next := Snapshot{
		Sessions: []SessionView{idleView("z")},
		Cards:    []board.Card{{Path: "/board/new.md", Stage: "blocked"}},
	}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a brand-new card must not fire even if already blocked: %+v", fire)
	}
}

// TestSessionBrandNewAlreadyWaitingDoesFire: unlike a card, a session that
// appears for the first time already Waiting is itself news the operator has
// not been told about yet, so it fires even though this particular session
// has no prior entry to compare against.
func TestSessionBrandNewAlreadyWaitingDoesFire(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("z")}}
	next := Snapshot{Sessions: []SessionView{idleView("z"), waitingView("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:waiting" {
		t.Fatalf("a session appearing already waiting must fire: %+v", fire)
	}
}

// TestZeroValuedPreviousSnapshotFiresNothing: the very first tick has no prior
// observation to compare against, so every standing state in next would look brand
// new. A zero-valued prev — never assembled, At unset — is that case, and it is the
// only case that stays quiet.
func TestZeroValuedPreviousSnapshotFiresNothing(t *testing.T) {
	next := Snapshot{At: observedAt, Sessions: []SessionView{waitingView("a"), waitingView("b")}}
	fire, cleared := Diff(Snapshot{}, next, time.Hour)
	if len(fire) != 0 || len(cleared) != 0 {
		t.Fatalf("the first snapshot has nothing to compare against and must stay quiet, got fire=%+v cleared=%v", fire, cleared)
	}
}

// TestPreviousSnapshotOfAnEmptyFleetStillDiffs: an empty fleet is a real
// observation, not the absence of one. A tick that saw no sessions and no cards, and
// a tick that never happened, are different facts, and only the second may be quiet:
// spec section 1 exists for exactly the session that appears already waiting and is
// not noticed (three sessions stood for two hours). Keying the guard on "prev has no
// sessions and no cards" swallowed that first appearance.
func TestPreviousSnapshotOfAnEmptyFleetStillDiffs(t *testing.T) {
	prev := Snapshot{At: observedAt}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{waitingView("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:waiting" {
		t.Fatalf("a session appearing already waiting after an observed empty fleet must fire: %+v", fire)
	}
}

func TestEventTextIsEnglish(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{waitingView("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Text != "is waiting for an answer" {
		t.Fatalf("interface text must be English per spec section 10.1: %+v", fire)
	}
}

// TestZeroSilenceThresholdDisablesTheSilenceRule pins task-9-fix-round-1 item 1:
// spec line 248 defines notify.silence_after as the threshold a session must be
// silent for before it counts as an event, so a zero threshold cannot mean "every
// silence is an event" — every live session crosses zero on first sight and the
// whole fleet gets a banner. Zero means the rule is off.
func TestZeroSilenceThresholdDisablesTheSilenceRule(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("z")}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{idleView("z"), idleView("a"), idleView("b")}}
	fire, cleared := Diff(prev, next, 0)
	if len(fire) != 0 {
		t.Fatalf("a zero silence threshold turns the silence rule off, it must not fire: %+v", fire)
	}
	if len(cleared) != 0 {
		t.Fatalf("a disabled silence rule must not clear anything either: %v", cleared)
	}
}

// TestSessionThatDisappearsReleasesItsKeys pins task-9-fix-round-1 item 3: Diff used
// to walk next only, so a waiting session that ended left session:<short>:waiting
// standing in the notifier forever, and the same short id reappearing waiting was
// swallowed as "still waiting" — nobody gets called.
func TestSessionThatDisappearsReleasesItsKeys(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{At: observedAt.Add(time.Second)}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a session that ended fires nothing of its own: %+v", fire)
	}
	if len(cleared) != 1 || cleared[0] != "session:a:waiting" {
		t.Fatalf("a session that ended must release the key it was standing on: %v", cleared)
	}
}

func TestCardThatDisappearsReleasesItsKey(t *testing.T) {
	prev := Snapshot{At: observedAt, Cards: []board.Card{{Path: "/b/c.md", Stage: "blocked"}}}
	next := Snapshot{At: observedAt.Add(time.Second)}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a card that was deleted fires nothing of its own: %+v", fire)
	}
	if len(cleared) != 1 || cleared[0] != "card:/b/c.md:blocked" {
		t.Fatalf("a deleted card must release the key it was standing on: %v", cleared)
	}
}

// TestSessionStandingOnTwoRulesReleasesBoth: the keys a vanished session releases are
// every key it was standing on, not just the first one found.
func TestSessionStandingOnTwoRulesReleasesBoth(t *testing.T) {
	loud := waitingView("a")
	loud.SilentFor = 45 * time.Minute
	prev := Snapshot{At: observedAt, Sessions: []SessionView{loud}}
	next := Snapshot{At: observedAt.Add(time.Second)}
	_, cleared := Diff(prev, next, 30*time.Minute)
	want := []string{"session:a:waiting", "session:a:silent"}
	if len(cleared) != len(want) {
		t.Fatalf("a session standing on two rules must release both: %v", cleared)
	}
	for i, w := range want {
		if cleared[i] != w {
			t.Fatalf("cleared keys must come out in a fixed order, want %v got %v", want, cleared)
		}
	}
}

// TestCardWithParseErrorIsSkippedByTheStageRules pins item 4: board.Scan reports a
// card it could not parse as Card{Path, ParseError} with an empty Stage. Read as a
// stage, a half-written file is "left blocked" and the finished write on the next tick
// is "entered blocked": one edit, two banners.
func TestCardWithParseErrorIsSkippedByTheStageRules(t *testing.T) {
	prev := Snapshot{At: observedAt, Cards: []board.Card{{Path: "/b/c.md", Stage: "blocked"}}}
	next := Snapshot{At: observedAt.Add(time.Second), Cards: []board.Card{{Path: "/b/c.md", ParseError: "no frontmatter block"}}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("an unparseable card has no stage to have moved to: %+v", fire)
	}
	if len(cleared) != 0 {
		t.Fatalf("an unparseable card has not left its stage, it is only unreadable right now: %v", cleared)
	}
}

// TestDyingSessionFiresNothing pins item 5. Waiting() and Stalled() already exclude a
// Dying session with a stated reason — the job is being killed or retired, so nobody
// has to act on it. The failed and silent rules never got the same treatment, so
// killing a session yourself earned "ended in failure" and then "has been silent".
func TestDyingFailedSessionFiresNothing(t *testing.T) {
	dying := idleView("a")
	dying.State = "failed"
	dying.Dying = true
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{dying}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a session being killed must not report the kill as a failure: %+v", fire)
	}
}

func TestDyingSilentSessionFiresNothing(t *testing.T) {
	dying := idleView("a")
	dying.SilentFor = 45 * time.Minute
	dying.Dying = true
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{dying}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 0 {
		t.Fatalf("a session being retired is not silent, it is over: %+v", fire)
	}
}

func TestWaitingSessionThatStartsDyingClearsItsWaitingKey(t *testing.T) {
	dying := waitingView("a")
	dying.Dying = true
	prev := Snapshot{At: observedAt, Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{dying}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a dying session fires nothing: %+v", fire)
	}
	if len(cleared) != 1 || cleared[0] != "session:a:waiting" {
		t.Fatalf("nobody has to answer a session being killed, so its waiting key must be released: %v", cleared)
	}
}

// TestSessionsWithNoShortIDGetNoEventKeys pins item 6: the event key is built from
// Short, so two sessions with an empty Short collapse into one map entry and one
// meaningless key, "session::waiting".
func TestSessionsWithNoShortIDGetNoEventKeys(t *testing.T) {
	one := waitingView("")
	two := waitingView("")
	two.Name = "the other one"
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("z")}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{idleView("z"), one, two}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 || len(cleared) != 0 {
		t.Fatalf("a session with no short id cannot be keyed, so it produces no events: fire=%+v cleared=%v", fire, cleared)
	}
}

// TestStalledSessionSilentPastThresholdStillFires pins the silence rule to stalled
// sessions, not only running ones. Spec section 1's recorded case is exactly this:
// three sessions stood for two hours after the usage limit that stalled them had
// already reset, because nobody noticed. Stalled has no banner of its own (see Diff's
// doc comment) precisely because it resolves itself — but when it does not resolve,
// the silence rule is the one thing left that calls a person, and excluding stalled
// sessions from it would restore the two-hour silence the panel exists to end.
func TestStalledSessionSilentPastThresholdStillFires(t *testing.T) {
	before := stalledView("a")
	before.SilentFor = 10 * time.Minute
	after := stalledView("a")
	after.SilentFor = 2 * time.Hour
	prev := Snapshot{At: observedAt, Sessions: []SessionView{before}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{after}}
	if !after.Stalled() {
		t.Fatal("fixture must be a stalled session")
	}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 1 || fire[0].Key != "session:a:silent" {
		t.Fatalf("a stalled session silent past the threshold must still fire: %+v", fire)
	}
}

// TestCardRecoveringFromParseErrorIsNotANewStage covers the other half of item 4: the
// tick after the unparseable one. The card was never read as "" — an empty Stage is
// the absence of a reading, not a stage it stood in — so the finished write must not
// come out as "entered blocked". Skipping the card only on the next side would leave
// that phantom empty stage in the prev map and fire the second of the two banners the
// skip exists to prevent.
func TestCardRecoveringFromParseErrorIsNotANewStage(t *testing.T) {
	prev := Snapshot{At: observedAt, Cards: []board.Card{{Path: "/b/c.md", ParseError: "no frontmatter block"}}}
	next := Snapshot{At: observedAt.Add(time.Second), Cards: []board.Card{{Path: "/b/c.md", Stage: "blocked"}}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a card that only just became readable again has not moved anywhere: %+v", fire)
	}
	if len(cleared) != 0 {
		t.Fatalf("nothing was standing to be cleared: %v", cleared)
	}
}

// TestEventKindMapsOntoTheConfigToggles pins item 8. internal/config has four
// notify.enabled toggles — waiting, failed, silent, card_blocked — and deciding
// whether an event is allowed to fire meant parsing Key by suffix, with two different
// suffixes ("blocked" and "review") behind the single card_blocked toggle. Kind states
// the answer instead of leaving it to be re-derived at every call site.
func TestEventKindMapsOntoTheConfigToggles(t *testing.T) {
	failed := idleView("f")
	failed.State = "failed"
	silent := idleView("s")
	silent.SilentFor = 45 * time.Minute

	prev := Snapshot{
		At:       observedAt,
		Sessions: []SessionView{idleView("w"), idleView("f"), idleView("s")},
		Cards: []board.Card{
			{Path: "/b/blocked.md", Stage: "active"},
			{Path: "/b/review.md", Stage: "active"},
		},
	}
	next := Snapshot{
		At:       observedAt.Add(time.Second),
		Sessions: []SessionView{waitingView("w"), failed, silent},
		Cards: []board.Card{
			{Path: "/b/blocked.md", Stage: "blocked"},
			{Path: "/b/review.md", Stage: "review"},
		},
	}
	fire, _ := Diff(prev, next, 30*time.Minute)

	byKey := map[string]Event{}
	for _, e := range fire {
		byKey[e.Key] = e
	}
	want := map[string]struct{ kind, short, path string }{
		"session:w:waiting":          {"waiting", "w", ""},
		"session:f:failed":           {"failed", "f", ""},
		"session:s:silent":           {"silent", "s", ""},
		"card:/b/blocked.md:blocked": {"card_blocked", "", "/b/blocked.md"},
		// A card entering review is governed by the card_blocked toggle too; the
		// stage itself stays visible in Key and Text.
		"card:/b/review.md:review": {"card_blocked", "", "/b/review.md"},
	}
	if len(fire) != len(want) {
		t.Fatalf("expected one event per rule, got %d: %+v", len(fire), fire)
	}
	for key, w := range want {
		got, ok := byKey[key]
		if !ok {
			t.Fatalf("missing event %s in %+v", key, fire)
		}
		if got.Kind != w.kind {
			t.Errorf("%s: Kind must name the config toggle, want %q got %q", key, w.kind, got.Kind)
		}
		if got.Short != w.short {
			t.Errorf("%s: Short want %q got %q", key, w.short, got.Short)
		}
		if got.Path != w.path {
			t.Errorf("%s: Path want %q got %q", key, w.path, got.Path)
		}
	}
}

// TestUnmeasuredSilenceNeverFires: a zero SilentFor means "not measured", never "not
// silent" (see Link's doc comment). Silence is the age of the last write to the
// session's transcript, and a session that just started, one transcript.Locate cannot
// find, or one from another backend has no transcript to measure — Collect leaves the
// field zero. Read as a measurement of zero it is simply below any real threshold, so
// the rule stays quiet; read the other way round, every session in its first second of
// life would be told it had been silent for half an hour.
func TestUnmeasuredSilenceNeverFires(t *testing.T) {
	fresh := idleView("a")
	fresh.SilentFor = 0
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("z")}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{idleView("z"), fresh}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 0 {
		t.Fatalf("a session whose silence was never measured must not fire the silence rule: %+v", fire)
	}
}

// TestHumanDurationReadsLikeAPerson pins item 10: the silence banner rendered the
// threshold with Go's own formatting, so a half-hour window came out as "has been
// silent for over 30m0s". A banner is read by a person at a glance.
func TestHumanDurationReadsLikeAPerson(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{90 * time.Minute, "1h 30m"},
		{2 * time.Hour, "2h"}, // a zero minute component is dropped, not printed
		{45 * time.Second, "45s"},
		{90 * time.Second, "1m 30s"},
		{2*time.Hour + 5*time.Second, "2h 5s"},
		{500 * time.Millisecond, "500ms"}, // below a second there is nothing to round to
	} {
		if got := humanDuration(tc.in); got != tc.want {
			t.Errorf("humanDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSilenceBannerTextUsesTheHumanDuration(t *testing.T) {
	before := idleView("a")
	before.SilentFor = time.Minute
	after := idleView("a")
	after.SilentFor = 2 * time.Hour
	prev := Snapshot{At: observedAt, Sessions: []SessionView{before}}
	next := Snapshot{At: observedAt.Add(time.Second), Sessions: []SessionView{after}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 1 || fire[0].Text != "has been silent for over 30m" {
		t.Fatalf("the banner must read the way a person writes a duration: %+v", fire)
	}
}
