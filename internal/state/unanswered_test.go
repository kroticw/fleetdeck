package state

import (
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/transcript"
)

// unansweredView is a session the daemon describes as having no question outstanding,
// while its transcript shows it has owed its next move, saying nothing, for the given
// time.
func unansweredView(short string, unanswered time.Duration) SessionView {
	v := idleView(short)
	v.UnansweredFor = unanswered
	v.SilentFor = unanswered
	return v
}

// The daemon's "no question outstanding" is words, and words come from the session. A
// session that has owed its next move and said nothing for long enough -- a call that
// has not come back, a result it has not acted on, a message it has not answered --
// has not been able to update those words, so its "no" is stale. The verdict for it is
// the one #138 gave a source that says nothing: not known. Not "yes": nobody
// established that a person is needed, only that the session could not say.
func TestSessionLeftUnansweredPastTheLimitIsUnknown(t *testing.T) {
	s := unansweredView("a", UnansweredLimit)
	if got := s.Waiting(); got != daemon.Unknown {
		t.Fatalf("a session silent while owing its move for %s must be unknown, got %s", UnansweredLimit, got)
	}
}

func TestSessionUnansweredUnderTheLimitIsStillNo(t *testing.T) {
	s := unansweredView("a", UnansweredLimit-time.Second)
	if got := s.Waiting(); got != daemon.No {
		t.Fatalf("under the limit is ordinary work, got %s", got)
	}
}

// Silence while owing nothing is a turn that has ended: the session said its last
// words and nobody has spoken to it since. The daemon's "no" describes that session
// exactly; a long silence there is the silence rule's to report, not this one's.
func TestSilentSessionThatOwesNothingIsStillNo(t *testing.T) {
	s := idleView("a")
	s.SilentFor = 3 * UnansweredLimit
	if got := s.Waiting(); got != daemon.No {
		t.Fatalf("a finished turn is not an unanswered one: got %s", got)
	}
}

// Words outrank everything else, as they already do the flags: a question the daemon
// relays is a yes, and a stall it names is a no, however long the session has owed.
func TestWordsStillDecideWhenLongUnanswered(t *testing.T) {
	asking := unansweredView("a", 2*UnansweredLimit)
	asking.Needs = daemon.Says("answer: Allow ssh to connect to a new host? (yes · no)")
	if got := asking.Waiting(); got != daemon.Yes {
		t.Fatalf("a question in words is yes, got %s", got)
	}

	limited := unansweredView("b", 2*UnansweredLimit)
	limited.Needs = daemon.Says("rate limited")
	if got := limited.Waiting(); got != daemon.No {
		t.Fatalf("a named stall is no, got %s", got)
	}
}

func TestDyingSessionLongUnansweredIsNo(t *testing.T) {
	s := unansweredView("a", 2*UnansweredLimit)
	s.Dying = true
	if got := s.Waiting(); got != daemon.No {
		t.Fatalf("a session being killed needs no one, got %s", got)
	}
}

func TestSourceWithoutNeedsStaysUnknownHoweverLongUnanswered(t *testing.T) {
	s := unansweredView("a", time.Minute)
	s.Needs = nil
	if got := s.Waiting(); got != daemon.Unknown {
		t.Fatalf("a source that never speaks needs is unknown already, got %s", got)
	}
}

// The call is a name for what is unanswered, never a condition for the verdict: a
// session frozen inside a Read has no call on disk at all, and must reach the same
// answer as one frozen inside a Bash that does.
func TestAVisibleCallIsNotRequiredForTheVerdict(t *testing.T) {
	seen := unansweredView("a", UnansweredLimit)
	seen.InCall = &transcript.Call{Tool: "Bash", Since: observedAt.Add(-UnansweredLimit)}
	unseen := unansweredView("b", UnansweredLimit)
	if seen.Waiting() != daemon.Unknown || unseen.Waiting() != daemon.Unknown {
		t.Fatalf("with or without a call on disk the answer is the same: %s / %s", seen.Waiting(), unseen.Waiting())
	}
}

// The banner side follows #138 unchanged: an unknown verdict raises no waiting banner of
// its own, and the silence rule -- measured from what the session said, so a message
// sent to it cannot reset it -- is what calls a person.
func TestLongUnansweredRaisesNoWaitingBannerButSilenceStillCalls(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{unansweredView("a", 2*time.Hour)}}
	fire, _ := Diff(prev, next, time.Hour)

	var silent bool
	for _, e := range fire {
		if e.Key == "session:a:waiting" {
			t.Fatalf("not known is not yes, so no waiting banner: %+v", fire)
		}
		if e.Key == "session:a:silent" {
			silent = true
		}
	}
	if !silent {
		t.Fatalf("a session silent past the threshold must still call someone: %+v", fire)
	}
}

// The limit is a measurement with two walls, and this pins both. Below: the longest a
// working session was seen owing its move on disk over the fourteen days to
// 2026-09-13, 651 seconds, and the worst case its parts can add up to -- a Bash at its
// 601-second ceiling inside a batch Claude Code writes only when it ends, after the
// model's own 338 seconds at p99.99 -- 939 seconds. Above: the thirty-minute silence
// default, which this exists to beat.
func TestUnansweredLimitSitsBetweenTheLongestWorkingStretchAndTheSilenceDefault(t *testing.T) {
	if UnansweredLimit <= 939*time.Second {
		t.Fatalf("the limit must clear the worst working stretch (939s), got %s", UnansweredLimit)
	}
	if UnansweredLimit >= 30*time.Minute {
		t.Fatalf("the limit must beat the thirty-minute silence default, got %s", UnansweredLimit)
	}
}
