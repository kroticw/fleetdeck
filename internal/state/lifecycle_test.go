package state

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

// A card whose session is stopped must not be reported as an orphan. The
// session exists, it is paused, and it comes back with its whole history —
// "this card lost its session" is a different statement about it, and a
// false one.
func TestOrphanCardsSilentWhenSessionIsStopped(t *testing.T) {
	sessions := []SessionView{{
		Session:   daemon.Session{Short: "stop1234"},
		Lifecycle: LifecycleStopped,
	}}
	cards := []board.Card{{Path: "/board/one.md", Session: "stop1234"}}

	if orphans := OrphanCards(sessions, cards); len(orphans) != 0 {
		t.Fatalf("a card naming a stopped session must not be orphaned: %v", orphans)
	}
	stopped := StoppedCards(sessions, cards)
	if len(stopped) != 1 || stopped[0] != "/board/one.md" {
		t.Fatalf("a card naming a stopped session must be reported as stopped: %v", stopped)
	}
}

// A dead session cannot be brought back, so its card really has lost what it
// pointed at and stays an orphan — the distinction the three states exist
// for, made where the board reads it.
func TestOrphanCardsReportsCardNamingSessionThatCannotResume(t *testing.T) {
	sessions := []SessionView{{
		Session:   daemon.Session{Short: "dead1234"},
		Lifecycle: LifecycleDead,
	}}
	cards := []board.Card{{Path: "/board/one.md", Session: "dead1234"}}

	orphans := OrphanCards(sessions, cards)
	if len(orphans) != 1 || orphans[0] != "/board/one.md" {
		t.Fatalf("a card naming a dead session must be orphaned: %v", orphans)
	}
	if stopped := StoppedCards(sessions, cards); len(stopped) != 0 {
		t.Fatalf("a dead session's card must not be reported as merely stopped: %v", stopped)
	}
}

// A view built by Link alone carries no lifecycle, and that has always meant
// a session the daemon is listing. Reading an unset value as anything else
// would orphan every card on the board the first time a caller forgot to
// stamp one.
func TestUnsetLifecycleReadsAsLive(t *testing.T) {
	v := SessionView{Session: daemon.Session{Short: "abc12345"}}
	if !v.Live() {
		t.Error("an unset lifecycle must read as live")
	}
	if v.Resumable() {
		t.Error("a live session has nothing to resume")
	}
	cards := []board.Card{{Path: "/board/one.md", Session: "abc12345"}}
	if orphans := OrphanCards([]SessionView{v}, cards); len(orphans) != 0 {
		t.Fatalf("an unstamped session must keep its card off the orphan list: %v", orphans)
	}
}

func TestLifecycleAnswersLiveAndResumable(t *testing.T) {
	for _, tc := range []struct {
		lifecycle string
		live      bool
		resumable bool
	}{
		{LifecycleLive, true, false},
		{LifecycleStopped, false, true},
		{LifecycleDead, false, false},
	} {
		v := SessionView{Lifecycle: tc.lifecycle}
		if v.Live() != tc.live {
			t.Errorf("%s: Live() = %v, want %v", tc.lifecycle, v.Live(), tc.live)
		}
		if v.Resumable() != tc.resumable {
			t.Errorf("%s: Resumable() = %v, want %v", tc.lifecycle, v.Resumable(), tc.resumable)
		}
	}
}

// The lifecycle has to survive the trip to the browser: it is the whole
// basis of what the session list and the board draw differently.
func TestLifecycleReachesTheWire(t *testing.T) {
	snap := Snapshot{
		Sessions: []SessionView{{
			Session:   daemon.Session{Short: "stop1234"},
			Lifecycle: LifecycleStopped,
		}},
		StoppedCards: []string{"/board/one.md"},
		JobsError:    "read job store: permission denied",
		At:           time.Now(),
	}
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"lifecycle":"stopped"`,
		`"stoppedCards":["/board/one.md"]`,
		`"jobsError":"read job store: permission denied"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%s missing from the wire: %s", want, body)
		}
	}
}

// A stopped session raises no banner. Its State is the last one it recorded
// before it went away, frozen: read as a live reading, a session that ended
// in failure would raise a "failed" banner on every poll forever, and one
// whose transcript stopped being written hours ago would raise a "silent"
// one on top of it.
func TestStoppedSessionRaisesNoBanner(t *testing.T) {
	stopped := SessionView{
		Session: daemon.Session{
			Short: "stop1234",
			State: "failed",
			Needs: daemon.Says("answer: are you there?"),
		},
		Lifecycle: LifecycleStopped,
		SilentFor: 4 * time.Hour,
	}
	if rules := standingRules(stopped, 30*time.Minute); len(rules) != 0 {
		t.Fatalf("a stopped session must stand on no rule, got %v", rules)
	}

	dead := stopped
	dead.Lifecycle = LifecycleDead
	if rules := standingRules(dead, 30*time.Minute); len(rules) != 0 {
		t.Fatalf("a dead session must stand on no rule, got %v", rules)
	}

	// The same session while it was still running stands on all three, so
	// the check above is about the lifecycle and not about the fixture
	// quietly failing to satisfy anything.
	live := stopped
	live.Lifecycle = LifecycleLive
	if rules := standingRules(live, 30*time.Minute); len(rules) != 3 {
		t.Fatalf("the same session alive must stand on every rule, got %v", rules)
	}
}
