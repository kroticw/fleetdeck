package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"
)

// A source that cannot say whether a session is waiting for a person must reach the
// panel as "do not know", never as "not waiting". These tests drive that through the
// production parser with a reply from such a source, rather than asserting it about a
// Session built by hand: what has to hold is that the *decode* of a reply with no
// `needs` key produces Unknown, and a struct literal skips exactly the step where that
// could go wrong.
//
// Today's daemon is not such a source -- it always sends the key, empty string and all
// (2.1.269, 7 live records of 7) -- which is why the control case below matters as much
// as the silent one: the change must not turn a session that genuinely is waiting into
// an "unknown", or it would have traded one wrong answer for another.

// silentSourceReply is a `list` reply from a source that does not speak `needs` at all:
// every other key the protocol defines is there, and that one is simply absent. This is
// what section 4 says absence looks like -- "a key that was never set for a given
// session is simply absent rather than present with an empty value" -- and what a
// second agent's daemon would look like if it had no such concept.
const silentSourceReply = `{"ok":true,"op":"list","jobs":[
{"short":"quiet001","nonce":"n1","sessionId":"11111111-1111-4111-8111-111111111111","pid":1,"attempt":1,
 "startedAt":1783900420704,"createdAt":1783900420700,"cwd":"/repo","backend":"other","tempo":"active",
 "state":"working","detail":"working away","intent":"do the thing","name":"q1","agent":"other",
 "cliVersion":"9.9.9","source":"shell"},
{"short":"quiet002","nonce":"n2","sessionId":"22222222-2222-4222-8222-222222222222","pid":2,"attempt":1,
 "startedAt":1783900420704,"createdAt":1783900420700,"cwd":"/repo","backend":"other","tempo":"blocked",
 "state":"blocked","detail":"awaiting a decision on the dependency version","intent":"do the thing",
 "name":"q2","agent":"other","cliVersion":"9.9.9","source":"shell"}
]}`

// speakingSourceReply is the control: the same two sessions from a source that does
// speak the field -- one with a real question outstanding, one with nothing to report.
const speakingSourceReply = `{"ok":true,"op":"list","jobs":[
{"short":"loud0001","nonce":"n1","sessionId":"33333333-3333-4333-8333-333333333333","pid":3,"attempt":1,
 "startedAt":1783900420704,"createdAt":1783900420700,"cwd":"/repo","backend":"daemon","tempo":"active",
 "state":"working","detail":"which colour","intent":"do the thing","name":"l1","agent":"claude",
 "cliVersion":"2.1.269","source":"shell","needs":"answer: Which colour should the probe use? (Red · Green · Blue)"},
{"short":"loud0002","nonce":"n2","sessionId":"44444444-4444-4444-8444-444444444444","pid":4,"attempt":1,
 "startedAt":1783900420704,"createdAt":1783900420700,"cwd":"/repo","backend":"daemon","tempo":"active",
 "state":"working","detail":"working away","intent":"do the thing","name":"l2","agent":"claude",
 "cliVersion":"2.1.269","source":"shell","needs":""}
]}`

// sessionsFromReply serves one canned reply over a fake socket and returns what the
// production parser makes of it. The replies above are written across several lines to
// stay readable; the wire takes one line, so they are compacted here rather than
// spelled out unreadably at the top of the file.
func sessionsFromReply(t *testing.T, reply string) []Session {
	t.Helper()

	var line bytes.Buffer
	if err := json.Compact(&line, []byte(reply)); err != nil {
		t.Fatalf("compacting the canned reply: %v", err)
	}
	line.WriteByte('\n')

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return line.Bytes()
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) { return "key", nil })
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	return sessions
}

func TestSilentSourceIsUnknownNotNotWaiting(t *testing.T) {
	sessions := sessionsFromReply(t, silentSourceReply)
	if len(sessions) != 2 {
		t.Fatalf("parsed %d sessions, want 2", len(sessions))
	}

	for _, s := range sessions {
		if s.Needs != nil {
			t.Errorf("short %q decoded needs as %q; an absent key must decode to nil, "+
				"or the silence becomes a statement at the parse boundary", s.Short, *s.Needs)
		}
		if got := s.Waiting(); got != Unknown {
			t.Errorf("Waiting() = %v for short %q, want unknown: the source never said, "+
				"and answering %q on its behalf is the panel telling someone nobody is waiting "+
				"when nobody looked", got, s.Short, got)
		}
	}
}

// The second record of the silent source carries state=blocked. Stalled still reads the
// flags when Needs has nothing to say, exactly as it did when Needs was an empty string
// rather than absent -- this change does not touch that rule, and this test is here to
// prove it did not move by accident.
func TestSilentSourceStalledRuleUnchanged(t *testing.T) {
	sessions := sessionsFromReply(t, silentSourceReply)

	byShort := map[string]Session{}
	for _, s := range sessions {
		byShort[s.Short] = s
	}
	if got := byShort["quiet001"].Stalled(); got {
		t.Errorf("quiet001 Stalled() = true, want false: neither flag is blocked")
	}
	if got := byShort["quiet002"].Stalled(); !got {
		t.Errorf("quiet002 Stalled() = false, want true: state=blocked with no words is rule 2")
	}
	// And the partition still holds: nothing is both.
	for _, s := range sessions {
		if s.Waiting() == Yes && s.Stalled() {
			t.Errorf("short %q is both Waiting and Stalled", s.Short)
		}
	}
}

// The control the acceptance asks for: a session that really is waiting must still be
// reported as waiting. A change that answered "unknown" everywhere would satisfy the
// test above and be just as useless as the bool it replaced.
func TestSpeakingSourceStillAnswersYesAndNo(t *testing.T) {
	sessions := sessionsFromReply(t, speakingSourceReply)
	if len(sessions) != 2 {
		t.Fatalf("parsed %d sessions, want 2", len(sessions))
	}

	byShort := map[string]Session{}
	for _, s := range sessions {
		byShort[s.Short] = s
	}

	waiting := byShort["loud0001"]
	if got := waiting.Waiting(); got != Yes {
		t.Errorf("Waiting() = %v for a session with a question outstanding, want yes", got)
	}

	// The distinction the pointer exists for: this daemon sent "needs": "", which is an
	// answer -- it looked, and there is no question. That is No, not Unknown. If these
	// two collapsed into one value the change would have bought nothing.
	quiet := byShort["loud0002"]
	if quiet.Needs == nil {
		t.Fatal(`loud0002 decoded "needs": "" as absent; present-and-empty must stay distinct from absent`)
	}
	if got := quiet.Waiting(); got != No {
		t.Errorf("Waiting() = %v for a daemon that said it has no question, want no", got)
	}
}

// Dying answers No even from a source that says nothing else: being killed is itself a
// statement that nobody has to act on the session, and it does not depend on Needs.
func TestDyingIsNoEvenWhenNothingElseIsKnown(t *testing.T) {
	s := Session{Dying: true, State: "blocked", Tempo: "blocked"}
	if got := s.Waiting(); got != No {
		t.Errorf("Waiting() = %v for a dying session, want no", got)
	}
	if s.Stalled() {
		t.Error("a dying session must not be Stalled either")
	}
}

// The zero value of the type means "nobody has said anything yet", which is the only
// safe default for a verdict: a Verdict that defaults to No would reintroduce the bug
// this type exists to remove, one level up, in any caller that forgets to assign it.
func TestZeroVerdictIsUnknown(t *testing.T) {
	var v Verdict
	if v != Unknown {
		t.Errorf("the zero Verdict is %v, want unknown", v)
	}
	if got := (Session{}).Waiting(); got != Unknown {
		t.Errorf("a Session nobody filled in reports %v, want unknown", got)
	}
}
