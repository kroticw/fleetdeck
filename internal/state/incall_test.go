package state

import (
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/transcript"
)

// inCallView is a session the daemon describes as having no question outstanding,
// while its transcript shows it inside one tool call and silent for the given time.
func inCallView(short, tool string, silent time.Duration) SessionView {
	v := idleView(short)
	v.InCall = &transcript.Call{Tool: tool, Since: observedAt.Add(-silent)}
	v.SilentFor = silent
	return v
}

// The daemon's "no question outstanding" is words, and words come from the session.
// A session standing inside one tool call has not been able to say anything since the
// call began, so after long enough its "no" is not an answer it could have updated --
// it is a stale one. The verdict for it is the one #138 gave a source that says
// nothing: not known. Not "yes": nobody established that a person is needed, only that
// the session cannot tell.
func TestSessionSilentInsideOneCallPastTheLimitIsUnknown(t *testing.T) {
	s := inCallView("a", "Read", CallSilenceLimit)
	if got := s.Waiting(); got != daemon.Unknown {
		t.Fatalf("a session silent inside one call for %s must be unknown, got %s", CallSilenceLimit, got)
	}
}

func TestSessionInsideOneCallUnderTheLimitIsStillNo(t *testing.T) {
	s := inCallView("a", "Bash", CallSilenceLimit-time.Second)
	if got := s.Waiting(); got != daemon.No {
		t.Fatalf("a call under the limit is ordinary work, got %s", got)
	}
}

// Silence outside a call is the model's side -- thinking, generating, or a turn that
// has ended. The daemon's words still describe that session, so they stand; a long
// silence there is the silence rule's to report, not this one's.
func TestSilentSessionNotInsideACallIsStillNo(t *testing.T) {
	s := idleView("a")
	s.SilentFor = 3 * CallSilenceLimit
	if got := s.Waiting(); got != daemon.No {
		t.Fatalf("no open call, so nothing stops the session from speaking: got %s", got)
	}
}

// Words outrank everything else, as they already do the flags: a question the daemon
// relays is a yes, and a stall it names is a no, however long the call has run.
func TestWordsStillDecideInsideALongCall(t *testing.T) {
	asking := inCallView("a", "Bash", 2*CallSilenceLimit)
	asking.Needs = daemon.Says("answer: Allow ssh to connect to a new host? (yes · no)")
	if got := asking.Waiting(); got != daemon.Yes {
		t.Fatalf("a question in words is yes, got %s", got)
	}

	limited := inCallView("b", "Bash", 2*CallSilenceLimit)
	limited.Needs = daemon.Says("rate limited")
	if got := limited.Waiting(); got != daemon.No {
		t.Fatalf("a named stall is no, got %s", got)
	}
}

func TestUnmeasuredSilenceInsideACallIsStillNo(t *testing.T) {
	s := inCallView("a", "Read", 0)
	if got := s.Waiting(); got != daemon.No {
		t.Fatalf("zero silence means not measured, never a long silence: got %s", got)
	}
}

func TestDyingSessionInsideALongCallIsNo(t *testing.T) {
	s := inCallView("a", "Read", 2*CallSilenceLimit)
	s.Dying = true
	if got := s.Waiting(); got != daemon.No {
		t.Fatalf("a session being killed needs no one, got %s", got)
	}
}

func TestSourceWithoutNeedsStaysUnknownInOrOutOfACall(t *testing.T) {
	s := inCallView("a", "Read", time.Minute)
	s.Needs = nil
	if got := s.Waiting(); got != daemon.Unknown {
		t.Fatalf("a source that never speaks needs is unknown already, got %s", got)
	}
}

// The banner side follows #138 unchanged: an unknown verdict raises no waiting banner of
// its own, and the silence rule -- now measured from what the session said, so a message
// sent to it cannot reset it -- is what calls a person.
func TestSilentInsideACallRaisesNoWaitingBannerButSilenceStillCalls(t *testing.T) {
	prev := Snapshot{At: observedAt, Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{inCallView("a", "mcp__claude-agents__send_message", 2*time.Hour)}}
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

// The limit is a measurement with two walls, and this pins both. Below: Bash, the one
// tool whose calls legitimately run for minutes, is moved to the background by Claude
// Code when its timeout runs out, and its timeout cannot exceed ten minutes -- 18 215
// Bash calls over the fourteen days to 2026-09-12 stopped at 601 seconds at the most.
// Above: the notification default of thirty minutes, which this exists to beat.
func TestCallSilenceLimitSitsBetweenTheLongestBoundedCallAndTheSilenceDefault(t *testing.T) {
	if CallSilenceLimit <= 10*time.Minute+time.Second {
		t.Fatalf("the limit must clear the longest bounded call (601s), got %s", CallSilenceLimit)
	}
	if CallSilenceLimit >= 30*time.Minute {
		t.Fatalf("the limit must beat the thirty-minute silence default, got %s", CallSilenceLimit)
	}
}
