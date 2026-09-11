package daemon

// testdata/list_sessions.json is an anonymised capture of a real `list` reply from a
// live daemon (cwd, name, sessionId, nonce, pid and timestamps replaced; needs/intent/
// detail rewritten to neutral text of the same shape). Three of its records are
// derived from that capture — the first three in the file (short "a1c92f04",
// "b2d83e15", "c3e94f26") — and are left exactly as captured, key-for-key, rather than
// tidied to match the protocol document: two of them ("b2d83e15", "c3e94f26") carry
// "needs": "" and "intent": "" explicitly, where docs/protocol/daemon-control-socket.md
// section 4 describes an unset key as simply absent. Go's encoding/json makes no
// distinction between an absent key and one present with the zero value, so this
// changes nothing about how the fixture parses or what it exercises; it is left as the
// daemon actually sent it, a faithful capture being worth more here than one that
// silently disagrees with what was observed. The fourth (short "e4fa5037": tempo=active, state=blocked,
// needs="") is added by hand, because the live capture used for this fixture did not
// happen to contain that form. It is nonetheless attested: this form was observed live
// on this machine, a session parked for roughly an hour with
// detail="awaiting user decision on a dependency version". Under the current rule
// (docs/protocol/daemon-control-socket.md section 5) a bare blocked flag with empty
// needs is Stalled, never Waiting — State and Tempo are set by a mechanism the session
// does not control, so this record cannot be told apart from one merely coordinating
// its own subagents by the flags alone, even though its Detail text reads like a
// person-facing decision. That gap is exactly why the protocol obliges Detail to be
// shown verbatim for a session stalled this way.
//
// The fifth (short "f5ab6148") is also added by hand, to cover the `"dying": true` key
// documented in docs/protocol/daemon-control-socket.md section 4: a job being
// killed or retired carries this extra key, and its absence on every other record here
// is exactly what is supposed to mean "alive". Its other fields are invented the same
// way as e4fa5037's: plausible values of the same shape, not drawn from a live capture.
//
// Three more (short "d4bc7159", "07ea8b3c", "918cf4a2") are invented the same way, to
// cover Waiting/Stalled's split of "needs is non-empty" into a value from the closed
// stalledNeedsPrefixes vocabulary versus everything else: a needs beginning "answer:",
// which is not in that vocabulary, with neither blocked flag (d4bc7159); a needs
// reporting a usage limit, which is in the vocabulary, with neither blocked flag
// (07ea8b3c); and a tempo=blocked session whose needs matches the vocabulary too
// ("rate limited...") -- proving Needs decides regardless of the flags, so this one
// lands in Stalled, not Waiting, even with tempo=blocked (918cf4a2).
//
// The eighth (short "2b91d6f7") is invented the same way, to cover the blocker that
// Waiting/Stalled ignored Dying entirely: unlike f5ab6148 above (dying but otherwise
// plainly working), this one carries every trigger Waiting used to check for at once
// (state=blocked, tempo=blocked, and a "choose:" question in needs) alongside
// "dying": true, to prove Dying overrides all of them rather than merely the weakest.
// Under the current rule it also independently proves that a question needs outranks
// both blocked flags (were it not for Dying, this record's needs alone would already
// make it Waiting, per a1c92f04 below).
//
// Three more (short "6a3d8f52", "c7f2a916", "3e9b5c04") are invented the same way, to
// cover needs-driven cases the rest of the fixture does not: a needs matching
// stalledNeedsPrefixes with tempo=blocked, Stalled (6a3d8f52, same shape as
// 918cf4a2 but recorded directly against the fixture rather than only as a unit test);
// a needs with an unfamiliar prefix, not in the closed vocabulary, with neither
// blocked flag, which must be Waiting because the vocabulary is closed on purpose
// (c7f2a916); and a needs beginning "request too large", which the design spec's
// closed list deliberately omits (fixing it takes a person running /compact inside
// the session), which must also be Waiting (3e9b5c04).
//
// The twelfth (short "8b4e2f71") is invented the same way, to cover the flags-only
// side of the current rule with tempo, rather than state, carrying the blocked flag:
// tempo=blocked, needs="", state="working". This must be Stalled, not Waiting, for the
// same reason as e4fa5037 — State and Tempo cannot distinguish "waiting on a person"
// from "waiting on my own subagents" — and it carries a Detail of its own so the
// protocol's obligation to show it verbatim has something concrete to point at.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// serveOnce reads one request, processes it, and sends one response.
// It closes the connection after.
//
// This runs inside `go func() { serveOnce(...) }()` in about a dozen tests, so it must
// never call t.Fatalf: FailNow (which Fatalf calls) requires running on the test's own
// goroutine, and from any other goroutine it only runs runtime.Goexit on that
// goroutine — the test keeps running and the client sits blocked on its read until its
// own deadline, turning a clear failure into an unrelated-looking hang. Report with
// t.Error and return instead.
func serveOnce(t *testing.T, listener net.Listener, fn func(t *testing.T, req []byte) []byte) {
	t.Helper()
	conn, err := listener.Accept()
	if err != nil {
		t.Errorf("Accept: %v", err)
		return
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		t.Errorf("ReadString: %v", err)
		return
	}

	trimmed := strings.TrimSuffix(line, "\n")
	resp := fn(t, []byte(trimmed))
	if resp == nil {
		return
	}
	if _, err := conn.Write(resp); err != nil {
		t.Errorf("Write: %v", err)
	}
}

// loadFixtureLine reads the pretty-printed fixture and compacts it to the single-line
// form the real daemon actually sends over the wire (see the addendum: a response is
// one line terminated by '\n'). The fixture stays pretty-printed on disk for human review.
func loadFixtureLine(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/list_sessions.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		t.Fatalf("compacting fixture: %v", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

// TestFixtureParsesViaListSessions serves the fixture over a fake socket and feeds it
// through the production ListSessions parser, rather than unmarshalling it into a
// locally declared struct. Renaming the "jobs" key in client.go must break this test.
func TestFixtureParsesViaListSessions(t *testing.T) {
	line := loadFixtureLine(t)

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return line
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
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
	if len(sessions) == 0 {
		t.Fatal("fixture contains no sessions to test with")
	}
}

// TestWaitingAndStalledAgainstFixture parses the fixture through ListSessions and
// checks both Waiting() and Stalled() against a table of literal, explicit
// expectations keyed by each record's short id. Every entry is a fact asserted about
// that specific fixture record, not an expression recomputed from the Session's own
// fields — recomputing it would make the test agree with any definition of
// Waiting()/Stalled(), including a wrong one, since both sides change together.
func TestWaitingAndStalledAgainstFixture(t *testing.T) {
	line := loadFixtureLine(t)

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return line
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
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

	type expect struct{ waiting, stalled bool }
	// Every expectation below is derived directly from the current rule (see Waiting's
	// and Stalled's doc comments): needs non-empty decides alone, matching
	// stalledNeedsPrefixes or not; needs empty falls to state/tempo, which can only
	// ever produce Stalled or neither, never Waiting; Dying overrides both to neither.
	want := map[string]expect{
		// needs="choose: (1) ...": non-empty, does not match stalledNeedsPrefixes —
		// waiting, regardless of tempo=blocked and state=blocked both being set. This
		// is the record that proves words outrank flags: were needs read second, both
		// flags being blocked would suggest stalled, but the question text decides.
		"a1c92f04": {waiting: true, stalled: false},
		// needs="": empty, and neither flag is blocked — neither.
		"b2d83e15": {waiting: false, stalled: false},
		// needs="": empty, and neither flag is blocked — neither.
		"c3e94f26": {waiting: false, stalled: false},
		// needs="", state=blocked: needs is empty, so state/tempo decide — stalled,
		// never waiting, even though Detail's text ("awaiting user decision on a
		// dependency version") reads like a person-facing decision. State alone cannot
		// license that inference: it is set by a mechanism the session does not
		// control, indistinguishable from a session merely coordinating its own
		// subagents. This is exactly why the protocol obliges Detail to be shown
		// verbatim for a session stalled this way.
		"e4fa5037": {waiting: false, stalled: true},
		// needs="", dying=true: a job being retired. Dying overrides before either
		// needs or the flags are even read — plainly neither.
		"f5ab6148": {waiting: false, stalled: false},
		// needs="answer: ...": non-empty, does not match stalledNeedsPrefixes —
		// waiting, with neither flag blocked.
		"d4bc7159": {waiting: true, stalled: false},
		// needs="usage limit reached...": non-empty, matches stalledNeedsPrefixes —
		// stalled, not waiting, with neither flag blocked.
		"07ea8b3c": {waiting: false, stalled: true},
		// needs="rate limited...", tempo=blocked: non-empty, matches
		// stalledNeedsPrefixes — stalled. The flags play no part in this outcome at
		// all under the current rule; they are consulted only when needs is empty.
		"918cf4a2": {waiting: false, stalled: true},
		// needs="choose: ...", state=blocked, tempo=blocked, dying=true: every trigger
		// Waiting or Stalled could otherwise fire on is present at once, but Dying
		// overrides all of them — neither. Absent Dying, needs alone (non-empty, not
		// in stalledNeedsPrefixes) would already make this Waiting, per a1c92f04 above.
		"2b91d6f7": {waiting: false, stalled: false},
		// needs="rate limited...", tempo=blocked: the same shape as 918cf4a2, recorded
		// directly against the fixture — non-empty needs matching stalledNeedsPrefixes
		// makes this stalled regardless of tempo=blocked.
		"6a3d8f52": {waiting: false, stalled: true},
		// needs="disk full...": non-empty, an unfamiliar prefix not in
		// stalledNeedsPrefixes — the closed list is deliberately narrow, so this must
		// be waiting, not stalled, with neither flag blocked.
		"c7f2a916": {waiting: true, stalled: false},
		// needs="request too large...": non-empty, in the daemon's own vocabulary but
		// deliberately outside the design spec's closed list (fixing it takes a person
		// running /compact inside the session) — waiting, with neither flag blocked.
		"3e9b5c04": {waiting: true, stalled: false},
		// needs="", tempo=blocked: needs is empty, so state/tempo decide — stalled,
		// never waiting, the same reasoning as e4fa5037 but via Tempo rather than
		// State, and with a Detail whose own text explicitly denies being a
		// person-wait ("no reply needed from a person") — shown verbatim regardless,
		// since the protocol's obligation does not depend on what Detail happens to say.
		"8b4e2f71": {waiting: false, stalled: true},
	}

	if len(sessions) != len(want) {
		t.Fatalf("fixture has %d sessions but the expectation table has %d entries; keep them in sync", len(sessions), len(want))
	}

	for _, s := range sessions {
		exp, ok := want[s.Short]
		if !ok {
			t.Fatalf("fixture contains short %q, which is not in the expectation table", s.Short)
		}
		if got := s.Waiting(); got != exp.waiting {
			t.Errorf("Waiting() = %v for short %q, want %v", got, s.Short, exp.waiting)
		}
		if got := s.Stalled(); got != exp.stalled {
			t.Errorf("Stalled() = %v for short %q, want %v", got, s.Short, exp.stalled)
		}
		if s.Waiting() && s.Stalled() {
			t.Errorf("short %q is both Waiting and Stalled; the two must be mutually exclusive", s.Short)
		}
	}
}

// TestFixtureDyingFieldParsesViaListSessions covers the blocker that Session dropped the
// `dying` field entirely: encoding/json silently drops unknown keys, so a dying session
// arriving from ListSessions was indistinguishable from a live one. This asserts the one
// record carrying "dying": true parses as Dying == true, and every other record in the
// fixture parses as Dying == false.
func TestFixtureDyingFieldParsesViaListSessions(t *testing.T) {
	line := loadFixtureLine(t)

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return line
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
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

	wantDying := map[string]bool{
		"a1c92f04": false,
		"b2d83e15": false,
		"c3e94f26": false,
		"e4fa5037": false,
		"f5ab6148": true,
		"d4bc7159": false,
		"07ea8b3c": false,
		"918cf4a2": false,
		"2b91d6f7": true,
		"6a3d8f52": false,
		"c7f2a916": false,
		"3e9b5c04": false,
		"8b4e2f71": false,
	}

	if len(sessions) != len(wantDying) {
		t.Fatalf("fixture has %d sessions but the expectation table has %d entries; keep them in sync", len(sessions), len(wantDying))
	}

	for _, s := range sessions {
		expect, ok := wantDying[s.Short]
		if !ok {
			t.Fatalf("fixture contains short %q, which is not in the expectation table", s.Short)
		}
		if s.Dying != expect {
			t.Errorf("Dying = %v for short %q, want %v", s.Dying, s.Short, expect)
		}
	}
}

// TestFixtureFirstRecordAllFieldsLiteral covers the blocker that 15 of Session's 18
// fields were never asserted anywhere: a wrong json tag on any field but short/state/
// tempo would decode to a zero value and the suite would stay green. This asserts every
// field of the fixture's first record (short "a1c92f04") against its literal, expected
// value, parsed through the production ListSessions path.
func TestFixtureFirstRecordAllFieldsLiteral(t *testing.T) {
	line := loadFixtureLine(t)

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return line
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
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

	var got Session
	found := false
	for _, s := range sessions {
		if s.Short == "a1c92f04" {
			got = s
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fixture does not contain short \"a1c92f04\"")
	}

	want := Session{
		Short:      "a1c92f04",
		Nonce:      "f3e8c142",
		SessionID:  "a1c92f04-52b1-4b7d-9e3a-6f1d2c8b9a05",
		PID:        51234,
		Attempt:    1,
		StartedAt:  1783900420704,
		CreatedAt:  1783900420700,
		CWD:        "/home/user/project",
		Backend:    "daemon",
		Tempo:      "blocked",
		State:      "blocked",
		Detail:     "3 decisions needed: rollout strategy, config validation, release notes wording",
		Intent:     "read this and help decide on the rollout plan for the release",
		Name:       "", // absent on this record; absence must decode to the zero value
		Agent:      "claude",
		CLIVersion: "2.1.259",
		Source:     "fleet",
		Needs:      "choose: (1) deploy via two staged releases or one combined release; (2) confirm the default configuration value; (3) use the short title or the more descriptive one",
		Dying:      false,
	}

	if got != want {
		t.Errorf("record a1c92f04 =\n%+v\nwant\n%+v", got, want)
	}
}

// TestRateLimitedIsStalledNotWaiting covers the split between the two counters: a
// non-empty Needs that is not one of the question forms (as seen on a rate-limited or
// login-required session) is Stalled, since no answer fixes it, but never Waiting.
func TestRateLimitedIsStalledNotWaiting(t *testing.T) {
	s := Session{State: "working", Tempo: "active", Needs: "rate limited, retrying in 30s"}
	if s.Waiting() {
		t.Error("a rate-limited session with a non-question Needs must not be Waiting()")
	}
	if !s.Stalled() {
		t.Error("a rate-limited session with a non-question Needs must be Stalled()")
	}
}

// TestBareBlockedStateWithEmptyNeedsIsStalledNotWaiting covers the rule that a blocked
// flag alone, with no words in Needs, can never promote a session to Waiting: State
// and Tempo are set by a mechanism the session does not control, so a session
// reporting state=blocked cannot be told apart, by the flags alone, from one merely
// coordinating its own subagents. This holds even though Detail's text here
// ("awaiting a decision") reads exactly like a person-facing decision — Detail is not
// consulted by Waiting or Stalled at all; it is surfaced by the UI once a session
// lands in Stalled this way (see docs/protocol/daemon-control-socket.md section 5),
// not read by either method here.
func TestBareBlockedStateWithEmptyNeedsIsStalledNotWaiting(t *testing.T) {
	s := Session{State: "blocked", Tempo: "active", Needs: "", Detail: "awaiting a decision"}
	if s.Waiting() {
		t.Error("state=blocked with empty needs must never be Waiting(), regardless of tempo or detail")
	}
	if !s.Stalled() {
		t.Error("state=blocked with empty needs must be Stalled()")
	}
}

// TestBareBlockedTempoWithEmptyNeedsIsStalledNotWaiting covers the same rule via
// Tempo rather than State: tempo=blocked with empty needs is stalled, never waiting,
// for the identical reason — the flag alone cannot say whether a person or the
// session's own subagents are the reason for the stop.
func TestBareBlockedTempoWithEmptyNeedsIsStalledNotWaiting(t *testing.T) {
	s := Session{State: "working", Tempo: "blocked", Needs: ""}
	if s.Waiting() {
		t.Error("tempo=blocked with empty needs must never be Waiting()")
	}
	if !s.Stalled() {
		t.Error("tempo=blocked with empty needs must be Stalled()")
	}
}

// TestQuestionNeedsOutranksBothBlockedFlags covers the rule that a non-empty Needs not
// matching stalledNeedsPrefixes decides Waiting regardless of State or Tempo: with
// both flags blocked, a naive "check the flags" reading would call this stalled, but
// the question text in Needs makes it Waiting.
func TestQuestionNeedsOutranksBothBlockedFlags(t *testing.T) {
	s := Session{State: "blocked", Tempo: "blocked", Needs: "answer: Which colour should the probe use? (Red · Green · Blue)"}
	if !s.Waiting() {
		t.Error("a question needs must be Waiting() even with both state and tempo blocked")
	}
	if s.Stalled() {
		t.Error("a Waiting session must never also be Stalled")
	}
}

// TestWaitingQuestionNeedsAloneIsWaitingNotStalled covers the third, independent
// waiting form: neither flag is blocked, but the needs text itself is a question.
func TestWaitingQuestionNeedsAloneIsWaitingNotStalled(t *testing.T) {
	s := Session{State: "working", Tempo: "active", Needs: "choose: (1) A; (2) B"}
	if !s.Waiting() {
		t.Error("a \"choose:\" needs with neither flag blocked must be Waiting()")
	}
	if s.Stalled() {
		t.Error("a Waiting session must never also be Stalled")
	}
}

// TestWaitingNegative covers a session that is neither waiting nor stalled by any form.
func TestWaitingNegative(t *testing.T) {
	s := Session{State: "working", Tempo: "active", Needs: ""}
	if s.Waiting() {
		t.Error("state=working, tempo=active, needs=\"\" must not be waiting")
	}
	if s.Stalled() {
		t.Error("state=working, tempo=active, needs=\"\" must not be stalled either")
	}
}

// TestDyingSessionIsNeverWaitingOrStalled covers the blocker that a session being
// killed or retired still landed in the Waiting (or Stalled) counter: Dying was added
// specifically to tell a dying session from a live one, but Waiting/Stalled never
// consulted it. This session satisfies every one of Waiting's three triggers at once
// (state=blocked, tempo=blocked, and a "choose:" question in needs) to prove Dying
// overrides all of them, not just the weakest one.
func TestDyingSessionIsNeverWaitingOrStalled(t *testing.T) {
	s := Session{
		State: "blocked",
		Tempo: "blocked",
		Needs: "choose: (1) A; (2) B",
		Dying: true,
	}
	if s.Waiting() {
		t.Error("a dying session must never be Waiting(), regardless of state/tempo/needs")
	}
	if s.Stalled() {
		t.Error("a dying session must never be Stalled(), regardless of state/tempo/needs")
	}
}

// TestDialCheckedActuallyUsesConfiguredDialer covers blocker 2: dialChecked used to
// dial with a bare &net.Dialer{}, which carries no Timeout of its own, so a context
// with no deadline (context.Background() — the documented call path for ListSessions,
// SendText and Ping) left the connect itself
// unbounded. A daemon that is alive but not accepting connections (a full backlog, a
// wedged process) can make connect(2) on an AF_UNIX socket block indefinitely, and
// nothing could break a caller out of that.
//
// The previous version of this test never called dialChecked at all: it asserted
// 0 < dialTimeout <= 30s, properties of a constant that hold regardless of whether
// anything actually uses it. Deleting "Timeout: dialTimeout" from the Dialer
// dialChecked builds left that test green.
//
// This version calls dialChecked for real, against a live listener, and substitutes
// newControlDialer (the seam dialChecked now builds its Dialer through) to capture the
// *net.Dialer that call actually used. It asserts that Dialer's Timeout equals
// dialTimeout, so removing "Timeout: dialTimeout" from newControlDialer's construction
// fails this test.
//
// What this does NOT prove: that an unbounded connect(2) against a wedged, non-
// accepting daemon is actually interrupted after dialTimeout. On macOS, connecting to
// a listening-but-unaccepted unix socket returns immediately regardless of backlog
// state, so there is no local fixture that reproduces that hang to assert against.
// This test proves the wiring — the value in effect is the one the comments claim —
// not the runtime behaviour under a genuinely wedged peer.
func TestDialCheckedActuallyUsesConfiguredDialer(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	origNewControlDialer := newControlDialer
	var gotTimeout time.Duration
	sawCall := false
	newControlDialer = func() *net.Dialer {
		sawCall = true
		d := origNewControlDialer()
		gotTimeout = d.Timeout
		return d
	}
	defer func() { newControlDialer = origNewControlDialer }()

	conn, err := dialChecked(context.Background(), listener.Addr().String())
	if err != nil {
		t.Fatalf("dialChecked: %v", err)
	}
	defer conn.Close()

	if !sawCall {
		t.Fatal("dialChecked did not build its Dialer through newControlDialer")
	}
	if gotTimeout != dialTimeout {
		t.Errorf("dialChecked's Dialer.Timeout = %v, want dialTimeout (%v)", gotTimeout, dialTimeout)
	}
}

func TestMissingSocketErrDaemonUnavailable(t *testing.T) {
	client := New("/nonexistent/socket/path", func() (string, error) {
		return "key", nil
	})

	_, err := client.ListSessions(context.Background())
	if err == nil {
		t.Fatal("expected an error for a missing socket, got nil")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected ErrDaemonUnavailable, got %v", err)
	}
	if err.Error() == ErrDaemonUnavailable.Error() {
		t.Errorf("expected the wrapped error to carry the underlying cause, got %v", err)
	}
}

// TestTruncatedJSONIsError covers a truncated `list` reply. Without client.proto set,
// this used to consume the single serveOnce response as the answer to ensureProto's
// own ping (serveOnce only ever answers one request), so ListSessions failed with a
// JSON error from Ping — never reaching listSessionsOnce's own line-reading and
// struct-unmarshalling at all, despite the payload being shaped like a list reply and
// the test's own name. Setting client.proto = 1 makes the served response actually
// answer the "list" request this test is about, and asserting the error's text
// pins down that it is a JSON syntax error (from the truncated payload itself),
// not some other unrelated failure that also happens to be non-nil.
func TestTruncatedJSONIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			if err := json.Unmarshal(req, &capturedReq); err != nil {
				t.Errorf("unmarshalling captured request: %v", err)
			}
			// Truncated JSON (missing closing brackets/brace), but still terminated
			// with the framing newline readBoundedLine requires: this needs to reach
			// json.Unmarshal and fail there, on the malformed content, rather than
			// failing earlier on missing framing (a bare io.EOF, which is what an
			// unterminated line like this would produce, and would defeat the point
			// of a test named for truncated *JSON*).
			return []byte(`{"ok":true,"op":"list","jobs":[` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1 // skip the ping, so the one served response answers "list"

	_, err = client.ListSessions(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatal("expected error for truncated JSON, got nil")
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Errorf("expected a JSON syntax error from the truncated payload, got %T: %v", err, err)
	}

	// Pin down that the exercised op was actually "list", not "ping": a malformed
	// reply to the ping ensureProto sends first would produce the exact same
	// *json.SyntaxError, which would make the assertion above pass for the wrong
	// reason (as it silently did before client.proto was set here).
	if op, _ := capturedReq["op"].(string); op != "list" {
		t.Fatalf("request op was %q, not \"list\"; this test no longer exercises ListSessions' own error path", op)
	}
}

func TestSilentDaemonTimesOut(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Don't send anything; just hold the connection until the test cleans up.
		<-stop
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = client.ListSessions(ctx)
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestListSessionsCachedProtoTimesOut(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu        sync.Mutex
		connCount int
		hungConns []net.Conn
	)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range hungConns {
			c.Close()
		}
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			mu.Lock()
			connCount++
			n := connCount
			mu.Unlock()

			reader := bufio.NewReader(conn)
			_, _ = reader.ReadString('\n')

			if n == 1 {
				// First request (ping) - respond, then close.
				conn.Write([]byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n"))
				conn.Close()
				continue
			}

			// Second request (list with cached proto) - never respond, just hold
			// the connection open until the test cleans it up.
			mu.Lock()
			hungConns = append(hungConns, conn)
			mu.Unlock()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	// Set a short deadline so test doesn't wait 30 seconds
	client.defaultDeadline = 1 * time.Second

	// Prime the cache with a ping
	_, _ = client.Ping(context.Background())

	// Now call ListSessions with context.Background() (no deadline).
	// Without the deadline fallback, this would hang forever.
	// With it, it should timeout after defaultDeadline (1 second).
	_, err = client.ListSessions(context.Background())
	if err == nil {
		t.Error("expected timeout error for ListSessions with cached proto and no context deadline, got nil")
	}
}

func TestRequestEndsWithNewline(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var mu sync.Mutex
	requestLines := make([]string, 0)
	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		// done is closed via defer, on every return path (including a read error),
		// rather than only on the success path: closing it only after n>=2 left a read
		// failure with no signal at all, and the test below would burn its full 2s
		// timeout on a generic "did not capture both requests" message instead of the
		// actual, more specific read error captured in errChan.
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			reader := bufio.NewReader(conn)
			line, err := reader.ReadString('\n')
			if err != nil {
				conn.Close()
				errChan <- err
				return
			}
			mu.Lock()
			requestLines = append(requestLines, line)
			n := len(requestLines)
			mu.Unlock()

			// Send a ping response on the first request
			if n == 1 {
				conn.Write([]byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n"))
			} else {
				// Send a list response
				conn.Write([]byte(`{"ok":true,"op":"list","jobs":[]}` + "\n"))
			}
			conn.Close()

			if n >= 2 {
				return
			}
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	// Trigger requests
	client.Ping(context.Background())
	client.ListSessions(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not capture both requests")
	}
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify we captured the expected number of requests.
	// If framing broke and requests never ended with \n, ReadString would hang forever
	// and requestLines would be empty. Zero captured requests means the framing hung.
	if len(requestLines) != 2 {
		t.Fatalf("expected 2 captured requests (ping + list), got %d; zero means framing hung", len(requestLines))
	}

	// Verify each request ends with exactly one \n
	for i, line := range requestLines {
		if !strings.HasSuffix(line, "\n") {
			t.Errorf("request %d does not end with newline: %q", i, line)
		}
		// Verify only one newline at the end
		if strings.Count(line, "\n") != 1 {
			t.Errorf("request %d has multiple newlines: %q", i, line)
		}
	}
}

func TestProtoNegotiation(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var mu sync.Mutex
	requestProtos := make([]interface{}, 0)
	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			serveOnce(t, listener, func(_ *testing.T, req []byte) []byte {
				var m map[string]interface{}
				if err := json.Unmarshal(req, &m); err != nil {
					errChan <- err
					return nil
				}
				mu.Lock()
				requestProtos = append(requestProtos, m["proto"])
				mu.Unlock()

				if i == 0 { // ping response
					return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":7}` + "\n")
				}
				// list response
				return []byte(`{"ok":true,"op":"list","jobs":[]}` + "\n")
			})
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	client.Ping(context.Background())
	client.ListSessions(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not process both requests")
	}

	// Check for any errors from the goroutine
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()

	// First request (ping) should have no proto field
	if proto := requestProtos[0]; proto != nil {
		t.Errorf("ping request should not have proto field, got %v", proto)
	}

	// Second request (list) should have proto 7 from the ping response
	if proto := requestProtos[1]; proto != float64(7) {
		t.Errorf("list request should have proto 7, got %v", proto)
	}
}

func TestPingNoProtoField(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			var m map[string]interface{}
			if err := json.Unmarshal(req, &m); err != nil {
				errChan <- err
				return nil
			}

			if _, ok := m["proto"]; ok {
				t.Error("ping request should not have proto field")
			}
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	client.Ping(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}

	// Check for any errors from the goroutine
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}
}

// TestPingReturnsDaemonVersion covers Info.Version: nothing else in this file asserts
// on it at all, so deleting the line in Ping that populates it from the reply's
// "version" field leaves every other test green.
func TestPingReturnsDaemonVersion(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":7}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if info.Version != "2.1.263" {
		t.Fatalf("expected Info.Version %q, got %q", "2.1.263", info.Version)
	}
}

// TestPingMissingProtoIsError covers the case that used to silently produce
// Info{Proto: 0}, nil: a ping reply with no proto field at all. Proto 0 is
// indistinguishable from "not yet negotiated" in the client's cache, so accepting it
// would make ensureProto re-ping on every subsequent call.
func TestPingMissingProtoIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatalf("expected an error for a ping reply with no proto field, got Info=%+v", info)
	}
	if info.Proto != 0 {
		t.Errorf("expected a zero Info on error, got %+v", info)
	}
}

// TestPingNonNumberProtoIsError covers a proto field present but not a number.
func TestPingNonNumberProtoIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":"1"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatalf("expected an error for a non-numeric proto field, got Info=%+v", info)
	}
	if info.Proto != 0 {
		t.Errorf("expected a zero Info on error, got %+v", info)
	}
}

// TestPingProtoZeroIsError covers a reply of {"ok":true,"proto":0}: proto 0 is
// indistinguishable from "not yet negotiated" in the client's cache (see ensureProto),
// so accepting it would make ensureProto re-ping before every subsequent call — the
// exact traffic doubling the comment in Ping claims to prevent — and then send
// "proto": 0 on every request, earning an EPROTO from the daemon. Symmetric with
// TestPingMissingProtoIsError and TestPingNonNumberProtoIsError.
func TestPingProtoZeroIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":0}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatalf("expected an error for proto:0, got Info=%+v", info)
	}
	if info.Proto != 0 {
		t.Errorf("expected a zero Info on error, got %+v", info)
	}
}

// TestPingFractionalProtoIsError covers a reply like {"proto": 1.9}: checking only
// protoNum < 1 and then truncating with int(protoNum) would silently turn that into
// proto 1 — a value the daemon never actually reported. A non-integer proto is just as
// malformed as a missing or non-numeric one and must be rejected the same way,
// symmetric with TestPingMissingProtoIsError, TestPingNonNumberProtoIsError and
// TestPingProtoZeroIsError.
func TestPingFractionalProtoIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1.9}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatalf("expected an error for a fractional proto:1.9, got Info=%+v", info)
	}
	if info.Proto != 0 {
		t.Errorf("expected a zero Info on error, got %+v", info)
	}
}

// TestListSessionsErrorWithoutCodeIsCleanError covers an "ok":false list reply carrying
// no code field at all: it must produce a clean "unknown error", never the dangling
// "unknown error: " that daemonError(...) would otherwise build from an empty code.
func TestListSessionsErrorWithoutCodeIsCleanError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":false}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	_, err = client.ListSessions(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatal("expected an error for an ok:false reply with no code, got nil")
	}
	if err.Error() != "unknown error" {
		t.Errorf("expected a clean %q, got %q", "unknown error", err.Error())
	}
}

// --- resolveSocketCandidate: an ownership refusal must not collapse into a bare
// ErrDaemonUnavailable when every candidate is refused ---

// TestResolveSocketCandidateReportsOwnershipRefusal covers the one situation the
// client-side ownership check exists for: a foreign socket planted ahead of the real
// daemon. When every candidate is refused on ownership grounds, that refusal must be
// visible in the returned error, not silently discarded in favour of a bare
// ErrDaemonUnavailable that would read exactly like "the daemon just isn't running".
func TestResolveSocketCandidateReportsOwnershipRefusal(t *testing.T) {
	base := shortTempDir(t)
	insecureDir := filepath.Join(base, "insecure")
	if err := os.Mkdir(insecureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(insecureDir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	sockPath := filepath.Join(insecureDir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	_, err = resolveSocketCandidate([]string{sockPath})
	if err == nil {
		t.Fatal("expected an error when the only candidate fails the ownership check")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected the error to still satisfy errors.Is(err, ErrDaemonUnavailable), got %v", err)
	}
	// Asserted via the typed sentinel (errors.Is), not the prose: the exact wording
	// stays free to change without breaking this test.
	if !errors.Is(err, errSocketWritableByOthers) {
		t.Errorf("expected the ownership refusal to satisfy errors.Is(err, errSocketWritableByOthers), got %v", err)
	}
}

// TestResolveSocketCandidateSkipsDeadSocketAndUsesLiveOne covers the function's whole
// point, per its own doc comment: lexicographic order (what the candidates list is
// naturally sorted into by filepath.Glob) is not freshness. Given a dead socket ahead
// of a live one, the dead one must be skipped — not returned, and not treated as a
// fatal refusal — and the live one further down the list must be the one actually
// used. Every other resolveSocketCandidate test in this file exercises a refusal; none
// before this one exercised the success path at all.
func TestResolveSocketCandidateSkipsDeadSocketAndUsesLiveOne(t *testing.T) {
	base := shortTempDir(t)

	deadDir := filepath.Join(base, "a-dead") // sorts before "b-live" lexicographically
	if err := os.Mkdir(deadDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A correctly owned and moded socket file with nothing listening on it: it passes
	// checkSocketOwnership (so this exercises resolveSocketCandidate's *dial*-failure
	// skip specifically, not the earlier ownership-refusal skip a non-socket file
	// would hit instead — see checkSocketOwnership's own os.ModeSocket check) but
	// fails to dial, reproducing a crashed daemon's stale socket left behind on disk.
	// SetUnlinkOnClose(false) is what leaves the socket file in place after Close,
	// exactly as a crashed process (rather than a clean shutdown) would.
	deadSocket := filepath.Join(deadDir, "control.sock")
	deadListener, err := net.Listen("unix", deadSocket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadListener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := deadListener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	liveDir := filepath.Join(base, "b-live")
	if err := os.Mkdir(liveDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	liveSocket := filepath.Join(liveDir, "control.sock")
	liveListener, err := net.Listen("unix", liveSocket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer liveListener.Close()

	got, err := resolveSocketCandidate([]string{deadSocket, liveSocket})
	if err != nil {
		t.Fatalf("expected success by skipping the dead candidate, got error: %v", err)
	}
	if got != liveSocket {
		t.Errorf("expected the live socket %q to be chosen, got %q", liveSocket, got)
	}
}

// TestResolveSocketCandidateNoMatchesIsPlainUnavailable covers the ordinary case: no
// candidates at all (the daemon simply is not running) stays a bare ErrDaemonUnavailable
// with no refusal noise attached.
func TestResolveSocketCandidateNoMatchesIsPlainUnavailable(t *testing.T) {
	_, err := resolveSocketCandidate(nil)
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected ErrDaemonUnavailable, got %v", err)
	}
}

// TestResolveSocketCandidateReportsDialFailure covers a candidate that passes the
// ownership check but fails to dial: skipping it with a bare `continue` and no
// collected cause would leave the caller with a plain ErrDaemonUnavailable and no
// cause at all — throwing away the one piece of evidence resolveSocketCandidate exists
// to preserve. The candidate here is a real socket file with nothing listening on it
// (see SetUnlinkOnClose(false) below), not a plain regular file: checkSocketOwnership
// refuses the latter outright now (see its own os.ModeSocket check), which would
// exercise the ownership-refusal path this test does not intend to cover. This one
// passes checkSocketOwnership (correctly owned, tightly moded, and an actual socket)
// but fails to dial, since nothing is listening there.
func TestResolveSocketCandidateReportsDialFailure(t *testing.T) {
	base := shortTempDir(t)
	secureDir := filepath.Join(base, "secure")
	if err := os.Mkdir(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	deadSocket := filepath.Join(secureDir, "control.sock")
	deadListener, err := net.Listen("unix", deadSocket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadListener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := deadListener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	_, err = resolveSocketCandidate([]string{deadSocket})
	if err == nil {
		t.Fatal("expected an error when the only candidate fails to dial")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected the error to still satisfy errors.Is(err, ErrDaemonUnavailable), got %v", err)
	}
	if err.Error() == ErrDaemonUnavailable.Error() {
		t.Errorf("expected the dial failure's own cause to be included, got a bare %v", err)
	}
}

func TestErrorCodeEPROTO(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":false,"code":"EPROTO","error":"proto mismatch"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	_, err = client.Ping(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	var epErr *ErrProto
	if !errors.As(err, &epErr) {
		t.Errorf("expected *ErrProto, got %T: %v", err, err)
	}
}

// TestErrorCodeEAUTH covers EAUTH mapping on a path that actually carries a
// credential. ListSessions' request carries only proto and op — there is no auth
// field anywhere on that path — so with client.proto left at its zero value, this
// test previously exercised the *ping* that ensureProto sends first, not any op that
// authenticates: serveOnce only answers one request, and it answered that ping with
// EAUTH before ListSessions' own "list" request was ever sent. SendText (op "reply")
// is the path that actually puts the control key on the wire (see req["auth"] below);
// client.proto is pre-set here specifically so ensureProto skips the ping and the
// single serveOnce response answers the "reply" request itself.
func TestErrorCodeEAUTH(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			if err := json.Unmarshal(req, &capturedReq); err != nil {
				t.Errorf("unmarshalling captured request: %v", err)
			}
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "test-key", nil
	})
	client.proto = 1 // skip the ping, so the one served response answers "reply"

	err = client.SendText(context.Background(), "session123", "hello")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	var eaErr *ErrAuth
	if !errors.As(err, &eaErr) {
		t.Errorf("expected *ErrAuth, got %T: %v", err, err)
	}

	// Sanity check that this test actually exercises a path carrying the credential:
	// without this, a future change back to a credential-less op would make the
	// assertions above pass for the wrong reason again.
	if auth, _ := capturedReq["auth"].(string); auth != "test-key" {
		t.Fatalf("request never carried the control key (auth=%q); this test no longer exercises an authenticated path", auth)
	}

	// Error message should not contain the control key
	if strings.Contains(err.Error(), "test-key") {
		t.Error("error message should not contain control key")
	}
}

// TestErrorCodeEPEERUID covers EPEERUID surfacing from ListSessions' own "list" op.
// Without client.proto set, this used to exercise ensureProto's own ping instead:
// serveOnce only answers one request, and it answered that ping with EPEERUID before
// ListSessions' own "list" request was ever sent — the assertion below still passed
// (the same error type propagates either way), but for the wrong reason, exactly like
// TestErrorCodeEAUTH's own fix above. client.proto is pre-set here so ensureProto
// skips the ping and the one served response answers "list" itself; capturing the
// request and asserting its op is what actually pins that down.
func TestErrorCodeEPEERUID(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			if err := json.Unmarshal(req, &capturedReq); err != nil {
				t.Errorf("unmarshalling captured request: %v", err)
			}
			return []byte(`{"ok":false,"code":"EPEERUID","error":"peer uid mismatch"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1 // skip the ping, so the one served response answers "list"

	_, err = client.ListSessions(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	var epErr *ErrPeeruid
	if !errors.As(err, &epErr) {
		t.Errorf("expected *ErrPeeruid, got %T: %v", err, err)
	}

	// Sanity check that this test actually exercises the "list" op, not ping: without
	// this, a change back to consuming the ping's response would make the assertion
	// above pass for the wrong reason again.
	if op, _ := capturedReq["op"].(string); op != "list" {
		t.Fatalf("request op was %q, not \"list\"; this test no longer exercises ListSessions' own error path", op)
	}
}

func TestControlKeyMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	key, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

// TestControlKeyRefusesGroupReadableFile covers the also-fix item: ControlKey() did not
// check the key file's mode, while the socket path is checked exhaustively. Section 6 of
// the protocol document records the key as 0600 inside a 0700 directory; a group- or
// world-readable key file must be refused the same generic way a missing one is.
func TestControlKeyRefusesGroupReadableFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.WriteFile(keyPath, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0o640); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	key, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey for a group-readable key file, got %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

// TestControlKeyRefusesWorldReadableFile covers the other half of the same permission
// bits: a world-readable (but not group-readable) key file must be refused too.
func TestControlKeyRefusesWorldReadableFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.WriteFile(keyPath, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0o604); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	_, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey for a world-readable key file, got %v", err)
	}
}

// TestControlKeyRefusesWrongOwner covers the ownership half of checkKeyFileSecurity.
// checkKeyFileSecurity takes the expected uid as a parameter specifically so this can be
// tested without a second real user account: passing a deliberately wrong uid simulates
// the file being owned by someone else.
func TestControlKeyRefusesWrongOwner(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.WriteFile(keyPath, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	if err := checkKeyFileSecurity(keyPath, os.Getuid()+1); !errors.Is(err, errKeyFileWrongOwner) {
		t.Errorf("expected errKeyFileWrongOwner for a key file not owned by the expected uid, got %v", err)
	}
	// The real owner must still be accepted.
	if err := checkKeyFileSecurity(keyPath, os.Getuid()); err != nil {
		t.Errorf("expected a correctly owned and moded key file to be accepted, got: %v", err)
	}
}

// TestControlKeyAcceptsSecureFile is the positive control for the two refusal tests
// above: a correctly owned, mode-0600 key file must keep working exactly as before.
func TestControlKeyAcceptsSecureFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.WriteFile(keyPath, []byte("deadbeefdeadbeefdeadbeefdeadbeef\n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	key, err := ControlKey()
	if err != nil {
		t.Fatalf("expected success for a secure key file, got: %v", err)
	}
	if key != "deadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("expected the trimmed key value, got %q", key)
	}
}

// TestCheckKeyFileSecurityRefusesInsecureContainingDirectory covers the recommendation
// that checkKeyFileSecurity checked the key file's own owner and mode but stopped
// there, even though section 6 of the protocol document requires the containing
// directory to be mode 0700 too — unlike checkSocketOwnership's symmetric check on the
// socket path, which walks every enclosing directory. A correctly-moded control.key
// sitting inside a world-writable directory must still be refused: another user with
// write access to that directory could replace the key file itself (with, say, a
// symlink into a file only they control) between this check and ControlKey's read of
// it, and a check that only ever looks at the leaf file misses that entirely.
func TestCheckKeyFileSecurityRefusesInsecureContainingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.WriteFile(keyPath, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	err := checkKeyFileSecurity(keyPath, os.Getuid())
	if err == nil {
		t.Fatal("expected a correctly-moded key file inside a 0777 directory to be refused")
	}
	if !errors.Is(err, errKeyDirInsecureMode) {
		t.Errorf("expected errKeyDirInsecureMode, got %v", err)
	}
}

// TestStickyBitSatisfiesRootBoundary covers the also-fix item: checkSocketOwnership's
// early return for a root-owned enclosing directory was only sound while that directory
// carries the sticky bit (as /tmp's usual 1777 mode does) — without it, a local attacker
// could replace the whole cc-daemon-<uid> directory. This exercises the pure predicate
// directly, since faking a real root-owned test directory would need root.
func TestStickyBitSatisfiesRootBoundary(t *testing.T) {
	cases := []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{"not writable by group or other", 0o755, true},
		{"world-writable with sticky bit (/tmp's usual mode)", os.ModeSticky | 0o777, true},
		{"world-writable without sticky bit", 0o777, false},
		{"other-writable without sticky bit", 0o707, false},
		{"group-writable without sticky bit", 0o770, false},
	}
	for _, c := range cases {
		if got := stickyBitSatisfiesRootBoundary(c.mode); got != c.want {
			t.Errorf("%s: stickyBitSatisfiesRootBoundary(%v) = %v, want %v", c.name, c.mode, got, c.want)
		}
	}
}

// tempSocket creates a temporary unix socket path with a short name.
// macOS limits socket paths to 104 bytes, so we use a short directory name.
func tempSocket(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortTempDir(t), "s.sock")
}

// shortTempDir is t.TempDir(), except with a short name: t.TempDir() embeds the full
// test name, which for a unix socket a few directories deeper overflows macOS's ~104
// byte sun_path limit ("bind: invalid argument"). Use this instead of t.TempDir()
// wherever a socket will be created under the result.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fd")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	return dir
}

func TestSendTextRequest(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, req []byte) []byte {
			if err := json.Unmarshal(req, &capturedReq); err != nil {
				errChan <- err
				return nil
			}
			return []byte(`{"ok":true,"op":"reply"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "test-key-12345678", nil
	})
	client.proto = 1 // Set proto to avoid ping

	err = client.SendText(context.Background(), "session123", "hello world")
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server never processed the request")
	}
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	// Verify request structure
	if short, ok := capturedReq["short"].(string); !ok || short != "session123" {
		t.Errorf("expected short='session123', got %v", capturedReq["short"])
	}
	if text, ok := capturedReq["text"].(string); !ok || text != "hello world" {
		t.Errorf("expected text='hello world', got %v", capturedReq["text"])
	}
	if auth, ok := capturedReq["auth"].(string); !ok || auth != "test-key-12345678" {
		t.Errorf("expected auth='test-key-12345678', got %v", capturedReq["auth"])
	}
}

func TestSendTextKeyFunctionFailure(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu              sync.Mutex
		requestReceived bool
	)
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		conn, _ := listener.Accept()
		if conn != nil {
			mu.Lock()
			requestReceived = true
			mu.Unlock()
			conn.Close()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "", ErrNoControlKey
	})
	client.proto = 1

	// SendText returns ErrNoControlKey before it ever dials, so by the time this call
	// returns there is nothing left to wait for — a request could only exist if the
	// code above this comment were wrong, not because of timing.
	err = client.SendText(context.Background(), "session123", "hello")
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}

	// Deterministic, not a race against Accept: closing the listener now forces the
	// goroutine's Accept call to return immediately — with a real connection if one
	// had already arrived, or with a plain error if (as expected) none ever did —
	// rather than this check racing an Accept call that might not have run yet.
	listener.Close()
	<-accepted

	mu.Lock()
	defer mu.Unlock()
	if requestReceived {
		t.Error("server should not have received any request")
	}
}

func TestSendTextErrorENOJOB(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":false,"code":"ENOJOB","error":"no such session"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendText(context.Background(), "missing", "text")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	var nojobErr *ErrNojob
	if !errors.As(err, &nojobErr) {
		t.Errorf("expected *ErrNojob, got %T: %v", err, err)
	}
}

// TestConcurrentListSessionsRace exercises the proto cache under concurrent use: a
// *Client is shared between a poller and request handlers, so this must be clean
// under `go test -race`.
func TestConcurrentListSessionsRace(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				var m map[string]interface{}
				_ = json.Unmarshal([]byte(line), &m)
				switch m["op"] {
				case "ping":
					conn.Write([]byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1}` + "\n"))
				case "list":
					conn.Write([]byte(`{"ok":true,"op":"list","jobs":[]}` + "\n"))
				}
			}(conn)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
	})

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.ListSessions(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("ListSessions failed: %v", err)
	}
}

func TestErrorMessageNoControlKey(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"auth failed"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "super-secret-key-abc123xyz789", nil
	})
	client.proto = 1

	err = client.SendText(context.Background(), "session123", "text")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatal("expected an error for an EAUTH reply, got nil")
	}

	// The error message should not contain the control key
	if strings.Contains(err.Error(), "super-secret-key-abc123xyz789") {
		t.Error("error message contains control key value")
	}
}

func TestListSessionsMissingJobsKeyIsError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			// Reply with ok=true but no jobs field at all - malformed response
			return []byte(`{"ok":true,"op":"list"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Errorf("expected error for missing jobs field, got nil; sessions=%v", sessions)
	}
	if err != nil && !strings.Contains(err.Error(), "jobs") {
		t.Errorf("expected error to mention 'jobs' field, got: %v", err)
	}
}

func TestListSessionsEmptyJobsArrayIsNotError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			// Reply with ok=true and empty jobs array - valid response
			return []byte(`{"ok":true,"op":"list","jobs":[]}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Errorf("expected nil error for empty jobs array, got: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if sessions == nil {
		t.Error("expected empty slice for empty jobs array, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// --- New(sock, nil) must not panic (nil key function is substituted with a stub) ---

func TestNewNilKeyFuncSendTextReturnsErrNoControlKey(t *testing.T) {
	client := New("/nonexistent/socket/path", nil)
	client.proto = 1

	err := client.SendText(context.Background(), "session123", "hello")
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}
}

// --- EPROTO and dial retries (exactly once, never a loop) ---

// TestListSessionsRetriesOnceOnEPROTO covers a daemon that answers EPROTO to a stale
// cached proto: the call must renegotiate via a fresh ping and retry exactly once,
// with the retried request carrying the newly negotiated proto.
func TestListSessionsRetriesOnceOnEPROTO(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu            sync.Mutex
		requestProtos []interface{}
	)
	errChan := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				line, err := reader.ReadString('\n')
				if err != nil {
					errChan <- err
					return
				}
				var m map[string]interface{}
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					errChan <- err
					return
				}
				mu.Lock()
				requestProtos = append(requestProtos, m["proto"])
				mu.Unlock()

				switch m["op"] {
				case "ping":
					conn.Write([]byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":9}` + "\n"))
				case "list":
					if i == 0 {
						conn.Write([]byte(`{"ok":false,"code":"EPROTO"}` + "\n"))
					} else {
						conn.Write([]byte(`{"ok":true,"op":"list","jobs":[]}` + "\n"))
					}
				}
			}()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 5 // stale cached proto

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions failed after retry: %v", err)
	}
	if sessions == nil {
		t.Error("expected a non-nil sessions slice")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not process all requests")
	}
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requestProtos) != 3 {
		t.Fatalf("expected 3 requests (list, ping, list), got %d: %v", len(requestProtos), requestProtos)
	}
	if requestProtos[0] != float64(5) {
		t.Errorf("first list request should carry the stale cached proto 5, got %v", requestProtos[0])
	}
	if requestProtos[2] != float64(9) {
		t.Errorf("retried list request should carry the freshly negotiated proto 9, got %v", requestProtos[2])
	}
}

// TestDiscoverableClientRetriesOnceOnDeadSocket covers a discoverable client whose
// cached socket path has gone dead (the daemon restarted under a new directory): the
// call must re-resolve exactly once and succeed against the new path, and must cache
// the resolved path (via setSocketPath) rather than re-resolving on every subsequent
// call. A previous version of this test only asserted err == nil with no call counter
// on resolve at all, which a client that re-resolved on every single call — never
// caching anything — would also have passed.
func TestDiscoverableClientRetriesOnceOnDeadSocket(t *testing.T) {
	deadListener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadPath := deadListener.Addr().String()
	deadListener.Close() // now dead: nothing is listening, and the socket file is gone

	liveListener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer liveListener.Close()

	livePath := liveListener.Addr().String()
	go func() {
		for {
			conn, err := liveListener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				if _, err := reader.ReadString('\n'); err != nil {
					return
				}
				conn.Write([]byte(`{"ok":true,"op":"list","jobs":[]}` + "\n"))
			}(conn)
		}
	}()

	var resolveCalls int32
	client := New(deadPath, func() (string, error) { return "key", nil })
	client.proto = 1
	client.discoverable = true
	client.resolve = func() (string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return livePath, nil
	}

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if sessions == nil {
		t.Error("expected a non-nil sessions slice")
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Fatalf("expected resolve to be called exactly once, got %d", got)
	}
	if got := client.currentSocketPath(); got != livePath {
		t.Fatalf("expected the resolved path to be cached via setSocketPath, got %q, want %q", got, livePath)
	}

	// A second call must reuse the cached path (setSocketPath's whole point) rather
	// than re-resolving: resolveCalls must stay at 1.
	sessions, err = client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("second ListSessions failed: %v", err)
	}
	if sessions == nil {
		t.Error("expected a non-nil sessions slice on the second call")
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Fatalf("expected resolve still called exactly once after the cached path was reused, got %d", got)
	}
}

// TestNonDiscoverableClientDoesNotRetryDeadSocket covers the other half of Discover's
// contract: a client created with New (an explicit path) never re-resolves, so a dead
// socket stays a plain failure.
func TestNonDiscoverableClientDoesNotRetryDeadSocket(t *testing.T) {
	deadListener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadPath := deadListener.Addr().String()
	deadListener.Close()

	client := New(deadPath, func() (string, error) { return "key", nil })
	client.proto = 1

	_, err = client.ListSessions(context.Background())
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected ErrDaemonUnavailable, got %v", err)
	}
}

// TestDialDoesNotReResolveOnDeadContext covers a discoverable client whose first dial
// attempt fails because ctx is already done (context.Canceled or
// context.DeadlineExceeded), not because the socket is actually gone. c.resolve
// (SocketPath, for a real Discover-created client) takes no context and dials each
// glob candidate with its own fixed 500ms timeout, so re-resolving in this case just
// spends up to that long for no reason: the caller has already given up. dial must
// check ctx.Err() and return immediately, before ever calling resolve.
func TestDialDoesNotReResolveOnDeadContext(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	path := listener.Addr().String()

	var resolveCalls int32
	client := New(path, func() (string, error) { return "key", nil })
	client.discoverable = true
	client.resolve = func() (string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return path, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.dial(ctx)
	if err == nil {
		t.Fatal("expected dial against an already-cancelled context to fail")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected the dial error to still surface context.Canceled via errors.Is, got %v", err)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 0 {
		t.Errorf("expected dial not to re-resolve on an already-dead context, got %d resolve call(s)", got)
	}
}

// TestDialReturnsOriginalErrorWhenReResolveItselfFails covers the first of dial's two
// re-resolve failure branches: when c.resolve itself returns an error, dial must return
// the *original* dial failure (against the stale path), not the resolve error — the
// resolve error carries no information about why the socket was unreachable in the
// first place, and existing tests only ever exercise c.resolve succeeding.
func TestDialReturnsOriginalErrorWhenReResolveItselfFails(t *testing.T) {
	deadListener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadPath := deadListener.Addr().String()
	deadListener.Close() // now dead: nothing is listening, and the socket file is gone

	client := New(deadPath, func() (string, error) { return "key", nil })
	client.discoverable = true
	client.resolve = func() (string, error) {
		return "", errors.New("resolve boom")
	}

	_, err = client.dial(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected the original dial failure (ErrDaemonUnavailable) to surface, got %v", err)
	}
	if strings.Contains(err.Error(), "resolve boom") {
		t.Errorf("expected the original dial error, not the resolve error, got %v", err)
	}
}

// TestDialReturnsSecondDialErrorWhenReResolvedPathIsAlsoDead covers dial's other
// re-resolve failure branch: c.resolve succeeds with a new path, but that new path is
// itself unreachable. dial must report that second failure and must not cache the new
// path via setSocketPath — caching a path that never actually dialed successfully would
// make every subsequent call skip re-resolution against a path already known to be dead.
func TestDialReturnsSecondDialErrorWhenReResolvedPathIsAlsoDead(t *testing.T) {
	deadListener1, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadPath1 := deadListener1.Addr().String()
	deadListener1.Close()

	deadListener2, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadPath2 := deadListener2.Addr().String()
	deadListener2.Close()

	client := New(deadPath1, func() (string, error) { return "key", nil })
	client.discoverable = true
	var resolveCalls int32
	client.resolve = func() (string, error) {
		atomic.AddInt32(&resolveCalls, 1)
		return deadPath2, nil
	}

	_, err = client.dial(context.Background())
	if err == nil {
		t.Fatal("expected an error against a re-resolved path that is also dead")
	}
	if !errors.Is(err, ErrDaemonUnavailable) {
		t.Errorf("expected ErrDaemonUnavailable from the second dial attempt, got %v", err)
	}
	if got := atomic.LoadInt32(&resolveCalls); got != 1 {
		t.Errorf("expected resolve to be called exactly once, got %d", got)
	}
	if got := client.currentSocketPath(); got != deadPath1 {
		t.Errorf("expected the socket path to remain unchanged after a failed re-resolved dial, got %q, want %q", got, deadPath1)
	}
}

// --- checkSocketOwnership must accept the real production shape, not just the shape
// t.TempDir() happens to produce ---

// TestSocketPathFindsLiveDaemonSocket is a smoke test against whatever daemon socket
// actually exists on this machine, not a fake one under a test's own temp directory.
// Every other test in this file plants its socket under t.TempDir() (on macOS,
// /var/folders/.../T/...), and the walk in checkSocketOwnership stops at a root-owned,
// non-writable boundary (/var/folders/<hash>) before it ever reaches /var itself, which
// is a symlink to /private/var — so none of those tests exercise a symlinked ancestor
// at all. The real daemon socket lives under /tmp/cc-daemon-<uid>/<id>/control.sock, and
// on macOS /tmp is itself a symlink to /private/tmp: this is the one shape that actually
// matters in production, and it is exactly what SocketPath must resolve correctly.
//
// This must not print or assert the real path anywhere: the repository is public, and a
// path under a user's real /tmp is exactly the kind of thing that must never end up in a
// tracked test's output.
func TestSocketPathFindsLiveDaemonSocket(t *testing.T) {
	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	pattern := filepath.Join("/tmp", fmt.Sprintf("cc-daemon-%s", currentUser.Uid), "*", "control.sock")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("no live daemon socket on this machine; skipping")
	}

	// Deliberately not interpolating err into the failure message: its text can carry
	// the real socket path (see dialChecked's %w wrapping), and that path must never
	// land in this tracked test's output, including a future CI failure log.
	path, err := SocketPath()
	if err != nil {
		t.Fatal("SocketPath failed to find a live daemon socket that filepath.Glob found; see the daemon package's own error for the (locally reproducible, not logged here) cause")
	}
	if path == "" {
		t.Fatal("SocketPath returned an empty path despite a live socket existing")
	}
}

// --- Client-side socket ownership check ---

func TestCheckSocketOwnershipRefusesGroupOrOtherWritableDir(t *testing.T) {
	base := shortTempDir(t)
	insecureDir := filepath.Join(base, "insecure")
	if err := os.Mkdir(insecureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(insecureDir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	sockPath := filepath.Join(insecureDir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	err = checkSocketOwnership(sockPath)
	if err == nil {
		t.Fatal("expected refusal for a group/other writable enclosing directory")
	}
	if !strings.Contains(err.Error(), insecureDir) {
		t.Errorf("expected the error to name the offending path %q, got: %v", insecureDir, err)
	}
}

func TestCheckSocketOwnershipAcceptsSecureDir(t *testing.T) {
	base := shortTempDir(t)
	secureDir := filepath.Join(base, "secure")
	if err := os.Mkdir(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := filepath.Join(secureDir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	if err := checkSocketOwnership(sockPath); err != nil {
		t.Errorf("expected a correctly owned and moded socket to be accepted, got: %v", err)
	}
}

// TestCheckSocketOwnershipRefusesNonSocketFile covers the recommendation that
// checkSocketOwnership never checked the path actually was a socket at all: a
// correctly owned, tightly moded, ordinary regular file at the expected path used to
// pass every check here and fail only later, at dial. This is defence in depth, not a
// hole this closes on its own (net.Dial against a non-socket path fails regardless),
// but every other property this function checks is checked explicitly rather than
// assumed, and the path being a socket at all should be no different.
func TestCheckSocketOwnershipRefusesNonSocketFile(t *testing.T) {
	base := shortTempDir(t)
	secureDir := filepath.Join(base, "secure")
	if err := os.Mkdir(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	notASocket := filepath.Join(secureDir, "control.sock")
	if err := os.WriteFile(notASocket, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	err := checkSocketOwnership(notASocket)
	if err == nil {
		t.Fatal("expected a correctly owned and moded but non-socket file to be refused")
	}
	if !errors.Is(err, errSocketNotASocket) {
		t.Errorf("expected errors.Is(err, errSocketNotASocket), got: %v", err)
	}
}

// TestClientRefusesInsecureSocketDirectory confirms the check is actually wired into
// the connect path, not just callable in isolation.
func TestClientRefusesInsecureSocketDirectory(t *testing.T) {
	base := shortTempDir(t)
	insecureDir := filepath.Join(base, "insecure")
	if err := os.Mkdir(insecureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(insecureDir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	sockPath := filepath.Join(insecureDir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	client := New(sockPath, func() (string, error) { return "key", nil })
	_, err = client.ListSessions(context.Background())
	if err == nil {
		t.Fatal("expected ListSessions to refuse an insecurely-owned socket directory")
	}
	// Asserted via the typed sentinel (errors.Is), not the prose: the exact wording
	// stays free to change without breaking this test.
	if !errors.Is(err, errSocketWritableByOthers) {
		t.Errorf("expected an ownership refusal satisfying errors.Is(err, errSocketWritableByOthers), got: %v", err)
	}
}

// TestCheckSocketOwnershipUIDRefusesWrongOwnerSocketFile covers errSocketWrongOwner on
// the socket file itself, as opposed to an ancestor directory: existing tests only ever
// reached this sentinel through the directory walk. checkSocketOwnershipUID takes the
// expected uid as a parameter specifically so a mismatch can be produced deterministically
// (a wantUID no real file can ever have), without needing a second real user account.
func TestCheckSocketOwnershipUIDRefusesWrongOwnerSocketFile(t *testing.T) {
	secureDir := shortTempDir(t)
	sockPath := filepath.Join(secureDir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	err = checkSocketOwnershipUID(sockPath, os.Getuid()+123456)
	if err == nil {
		t.Fatal("expected refusal for a socket file not owned by the expected uid")
	}
	if !errors.Is(err, errSocketWrongOwner) {
		t.Errorf("expected errors.Is(err, errSocketWrongOwner), got: %v", err)
	}
}

// TestCheckSocketOwnershipUIDAllowsWritableSocketFile covers branch-review-13's
// recommendation 4: the socket file's own mode bits are deliberately not checked for
// group/other writability (only the enclosing directories are — see
// checkSocketOwnershipUID's own comment on why). Before this, a socket file writable by
// group or other in an otherwise-secure directory was refused with
// errSocketWritableByOthers — which meant a daemon started under a permissive umask
// (e.g. 002, producing srwxrwxr-x) was refused for a mode bit that grants a local
// attacker nothing beyond what the directory check already covers, and the refusal was
// hard to diagnose. This asserts the opposite of what this test used to assert: such a
// socket must now be accepted.
func TestCheckSocketOwnershipUIDAllowsWritableSocketFile(t *testing.T) {
	secureDir := shortTempDir(t)
	sockPath := filepath.Join(secureDir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	if err := os.Chmod(sockPath, 0o777); err != nil {
		t.Fatalf("chmod socket: %v", err)
	}

	if err := checkSocketOwnershipUID(sockPath, os.Getuid()); err != nil {
		t.Errorf("expected a socket file writable by group or other, in an otherwise-secure directory, to be accepted, got: %v", err)
	}
}

// --- checkSocketOwnershipWalk: branches only reachable through the injectable lstatFunc ---

// TestCheckSocketOwnershipWalkPropagatesLstatError covers the walk's own lstat failure
// branch — distinct from every symlink/ownership/mode sentinel below it — reached when
// the underlying lstat call itself fails (e.g. a path removed mid-walk).
func TestCheckSocketOwnershipWalkPropagatesLstatError(t *testing.T) {
	lstat := fakeLstat(map[string]dirStat{})

	err := checkSocketOwnershipWalk("/x/control.sock", "/does/not/exist", os.Getuid(), lstat)
	if err == nil {
		t.Fatal("expected the walk to propagate a plain lstat failure")
	}
	if errors.Is(err, errSocketSymlink) || errors.Is(err, errSocketWrongOwner) || errors.Is(err, errSocketWritableByOthers) {
		t.Errorf("expected a plain lstat error, not one of the ownership sentinels: %v", err)
	}
}

// TestCheckSocketOwnershipWalkRefusesRootOwnedDirMissingStickyBit covers
// errSocketMissingStickyBit: a root-owned ancestor that is writable by group or other
// but does not carry the sticky bit is not actually outside a local attacker's control
// (see stickyBitSatisfiesRootBoundary), unlike /tmp's usual 1777 mode.
func TestCheckSocketOwnershipWalkRefusesRootOwnedDirMissingStickyBit(t *testing.T) {
	uid := os.Getuid()
	lstat := fakeLstat(map[string]dirStat{
		"/tmp/cc-daemon-x": {uid: 0, mode: 0o777}, // world-writable, no sticky bit
	})

	err := checkSocketOwnershipWalk("/tmp/cc-daemon-x/control.sock", "/tmp/cc-daemon-x", uid, lstat)
	if err == nil {
		t.Fatal("expected refusal for a root-owned, world-writable ancestor missing the sticky bit")
	}
	if !errors.Is(err, errSocketMissingStickyBit) {
		t.Errorf("expected errors.Is(err, errSocketMissingStickyBit), got: %v", err)
	}
}

// TestCheckSocketOwnershipWalkRefusesWrongOwnerAncestor covers errSocketWrongOwner
// inside the walk itself (an ordinary, non-root, non-symlink ancestor owned by someone
// else) — distinct from TestCheckSocketOwnershipUIDRefusesWrongOwnerSocketFile, which
// covers the same sentinel on the socket file, not an ancestor directory.
func TestCheckSocketOwnershipWalkRefusesWrongOwnerAncestor(t *testing.T) {
	uid := os.Getuid()
	lstat := fakeLstat(map[string]dirStat{
		"/tmp/cc-daemon-x": {uid: uid + 1, mode: 0o700},
	})

	err := checkSocketOwnershipWalk("/tmp/cc-daemon-x/control.sock", "/tmp/cc-daemon-x", uid, lstat)
	if err == nil {
		t.Fatal("expected refusal for an ancestor owned by a different, non-root user")
	}
	if !errors.Is(err, errSocketWrongOwner) {
		t.Errorf("expected errors.Is(err, errSocketWrongOwner), got: %v", err)
	}
}

// TestCheckSocketOwnershipWalkAcceptsFilesystemRootBoundary covers the walk's
// `parent == path` termination — reached only if the chain of secure, non-root-owned
// ancestors runs all the way up to the filesystem root without ever meeting a
// root-owned directory first (every other test drives the walk to a root-owned
// boundary instead).
func TestCheckSocketOwnershipWalkAcceptsFilesystemRootBoundary(t *testing.T) {
	uid := os.Getuid()
	lstat := fakeLstat(map[string]dirStat{
		"/x": {uid: uid, mode: 0o700},
		"/":  {uid: uid, mode: 0o700},
	})

	err := checkSocketOwnershipWalk("/x/control.sock", "/x", uid, lstat)
	if err != nil {
		t.Errorf("expected the walk to accept reaching the filesystem root, got: %v", err)
	}
}

// TestCheckSocketOwnershipWalkRefusesTooManyAncestors covers errSocketTooManyAncestors,
// the 64-ancestor symlink-loop guard: a two-node cycle of root-owned symlinks (root
// cannot be impersonated, so each hop is individually accepted) that never terminates
// on its own, forcing the walk to give up rather than loop forever.
func TestCheckSocketOwnershipWalkRefusesTooManyAncestors(t *testing.T) {
	uid := os.Getuid()
	lstat := fakeLstat(map[string]dirStat{
		"/loop1": {uid: 0, symlink: true, target: "/loop2"},
		"/loop2": {uid: 0, symlink: true, target: "/loop1"},
	})

	err := checkSocketOwnershipWalk("/loop1/control.sock", "/loop1", uid, lstat)
	if err == nil {
		t.Fatal("expected refusal for a symlink chain that never terminates")
	}
	if !errors.Is(err, errSocketTooManyAncestors) {
		t.Errorf("expected errors.Is(err, errSocketTooManyAncestors), got: %v", err)
	}
}

// --- checkKeyDirSecurity: the directory half of checkKeyFileSecurity ---

// TestCheckKeyDirSecurityPropagatesLstatError covers the plain os.Lstat failure branch
// (e.g. the directory does not exist, or was removed between checking the key file and
// checking its parent) — distinct from every sentinel below it.
func TestCheckKeyDirSecurityPropagatesLstatError(t *testing.T) {
	err := checkKeyDirSecurity(filepath.Join(shortTempDir(t), "does-not-exist"), os.Getuid())
	if err == nil {
		t.Fatal("expected a plain error for a missing key directory")
	}
	if errors.Is(err, errKeyDirSymlink) || errors.Is(err, errKeyDirWrongOwner) || errors.Is(err, errKeyDirInsecureMode) {
		t.Errorf("expected a plain lstat error, not one of the sentinels: %v", err)
	}
}

// TestCheckKeyDirSecurityRefusesSymlink covers errKeyDirSymlink: unlike
// checkSocketOwnershipWalk's ancestor chain, ~/.claude/daemon has no legitimate
// production shape in which it is itself a symlink, so this is refused outright rather
// than resolved.
func TestCheckKeyDirSecurityRefusesSymlink(t *testing.T) {
	base := shortTempDir(t)
	actualDir := filepath.Join(base, "actual")
	if err := os.Mkdir(actualDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkedDir := filepath.Join(base, "daemon")
	if err := os.Symlink(actualDir, linkedDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err := checkKeyDirSecurity(linkedDir, os.Getuid())
	if !errors.Is(err, errKeyDirSymlink) {
		t.Errorf("expected errors.Is(err, errKeyDirSymlink), got: %v", err)
	}
}

// TestCheckKeyDirSecurityRefusesWrongOwner covers errKeyDirWrongOwner directly: wantUID
// is a parameter precisely so this mismatch can be produced deterministically, the same
// pattern checkKeyFileSecurity's own wrong-owner test already uses.
func TestCheckKeyDirSecurityRefusesWrongOwner(t *testing.T) {
	dir := shortTempDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	err := checkKeyDirSecurity(dir, os.Getuid()+123456)
	if !errors.Is(err, errKeyDirWrongOwner) {
		t.Errorf("expected errors.Is(err, errKeyDirWrongOwner), got: %v", err)
	}
}

// --- EKICKED: the daemon evicting an attacher must not look like a normal outcome ---

// TestWrapNoControlKeyNilCauseYieldsPlainSentinel covers wrapNoControlKey's own
// documented special case directly: a nil cause must yield the bare ErrNoControlKey
// value, identical via == (not merely errors.Is), rather than a pointless
// *errNoControlKeyWithCause wrapper around nothing. No production call site passes nil
// today (every one guards on err != nil first), so this is the only way to exercise it.
func TestWrapNoControlKeyNilCauseYieldsPlainSentinel(t *testing.T) {
	got := wrapNoControlKey(nil)
	if got != ErrNoControlKey {
		t.Errorf("expected the exact ErrNoControlKey value for a nil cause, got %v (%T)", got, got)
	}
}

// TestControlKeyNoHomeDirIsError covers ControlKey's os.UserHomeDir() failure branch:
// with $HOME unset, UserHomeDir fails on every platform this package supports, and that
// failure must still collapse to the same generic ErrNoControlKey as every other cause.
func TestControlKeyNoHomeDirIsError(t *testing.T) {
	t.Setenv("HOME", "")

	_, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("expected ErrNoControlKey when $HOME is unset, got %v", err)
	}
	if err.Error() != ErrNoControlKey.Error() {
		t.Errorf("user-facing text must stay exactly %q, got %q", ErrNoControlKey.Error(), err.Error())
	}
}

// TestControlKeyEmptyFileIsError covers the fifth of the five causes ControlKey's own
// comment enumerates: a key file that passes every ownership and mode check but holds
// no key at all (or only whitespace).
func TestControlKeyEmptyFileIsError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.WriteFile(keyPath, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	_, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("expected ErrNoControlKey for a whitespace-only key file, got %v", err)
	}
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "empty") {
		t.Errorf("expected the internal cause to say the file is empty, got %v", cause)
	}
}

// TestControlKeyReadFileErrorIsError covers os.ReadFile's own failure branch, reached
// only after checkKeyFileSecurity has already accepted the path — a directory at
// control.key's path, correctly owned and moded (0700, same bits checkKeyFileSecurity
// requires of a key file), passes every security check but fails to read as file
// content with a deterministic EISDIR, no race required.
func TestControlKeyReadFileErrorIsError(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatalf("mkdir control.key: %v", err)
	}

	_, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("expected ErrNoControlKey when the key path is unreadable as a file, got %v", err)
	}
}

// TestControlKeyWrapsCauseButKeepsGenericMessage covers the recommendation that
// ControlKey's user-facing error collapsed every distinct cause into a bare
// ErrNoControlKey with nothing behind it, making the failure impossible to diagnose.
// The user-facing text must stay exactly "control key unavailable" with no
// home-directory path, but the actual cause must still be reachable internally via
// errors.Unwrap.
func TestControlKeyWrapsCauseButKeepsGenericMessage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.WriteFile(keyPath, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0o640); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	_, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("expected ErrNoControlKey, got %v", err)
	}
	if err.Error() != ErrNoControlKey.Error() {
		t.Errorf("user-facing text must stay exactly %q, got %q", ErrNoControlKey.Error(), err.Error())
	}
	if strings.Contains(err.Error(), tmpDir) {
		t.Errorf("user-facing text must not name the home directory, got %q", err.Error())
	}

	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("expected the internal cause to be reachable via errors.Unwrap")
	}
	// errors.Is against the exact sentinel, not a substring match: both
	// errKeyFileInsecureMode's and errKeyDirInsecureMode's text contain "group or
	// other", so a substring match here would still pass if the two checks were
	// swapped — this fixture (a 0640 key file inside a secure 0700 directory) must
	// name the file's own sentinel specifically.
	if !errors.Is(cause, errKeyFileInsecureMode) {
		t.Errorf("expected errors.Is(cause, errKeyFileInsecureMode), got %q", cause.Error())
	}
}

// TestControlKeyRefusesSymlinkWithClearInternalCause covers the recommendation's
// related point: checkKeyFileSecurity uses Lstat, so a symlink's own mode (almost
// always 0777) made it fall into the "readable or writable by group or other" branch
// regardless of the symlink's target — a refusal that may well be right, but with a
// cause that gives the operator nothing to act on. The symlink case is now detected
// explicitly, with an internal cause that says so.
func TestControlKeyRefusesSymlinkWithClearInternalCause(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dir := filepath.Join(tmpDir, ".claude", "daemon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realKey := filepath.Join(tmpDir, "real.key")
	if err := os.WriteFile(realKey, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0o600); err != nil {
		t.Fatalf("write real key file: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.Symlink(realKey, keyPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("expected ErrNoControlKey for a symlinked key file, got %v", err)
	}
	cause := errors.Unwrap(err)
	// errors.Is against the exact sentinel: errKeyFileSymlink, errKeyDirSymlink and
	// every socket-side symlink sentinel all contain "symlink" in their text, so a
	// substring match here would still pass if this check were swapped with any of
	// those — this fixture (the key file itself is the symlink) must name
	// errKeyFileSymlink specifically.
	if cause == nil || !errors.Is(cause, errKeyFileSymlink) {
		t.Errorf("expected errors.Is(cause, errKeyFileSymlink), got %v", cause)
	}
}

// --- Kick detection must be a compound signal, not "the marker's last occurrence
// plus a close" ---

// --- dial must respect the caller's context ---

// TestDialRespectsCanceledContext covers dial (and dialChecked) needing to honour the
// caller's context during the connect itself, not only during the request/response
// exchange that follows it: neither the 2s screen ceiling nor any context.WithTimeout
// a caller supplies is worth anything if the connect phase ignores it entirely. A
// daemon that is alive but not accepting (a full backlog, a wedged process) can make
// connect(2) on an AF_UNIX socket block indefinitely.
//
// A genuine "full accept backlog blocks connect" fixture proved impractical on this
// platform: verified empirically (a raw socket bound and listened with backlog 1, left
// unaccepted) that connect(2) against a full backlog on macOS returns ECONNREFUSED
// immediately rather than blocking, so there is nothing here for a context to
// interrupt. Instead, this proves the context actually reaches the dial by canceling
// it before the call and requiring the dial to fail with that cancellation, rather than
// ignoring it and succeeding or blocking.
func TestDialRespectsCanceledContext(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) { return "key", nil })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = client.dial(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled to be reachable via errors.Is, got %v", err)
	}
}

// --- checkSocketOwnership must reject a symlink explicitly, not rely on a symlink's
// mode bits conventionally reading as 0777 ---

// TestCheckSocketOwnershipRefusesSymlinkedSocketPath covers the socket path itself
// being a symlink to a real, correctly-owned socket elsewhere. checkKeyFileSecurity
// already refuses this shape explicitly for the control key file, with a clear
// internal cause; checkSocketOwnership must do the same instead of depending on the
// convention that a symlink's own Lstat mode reads as 0777.
func TestCheckSocketOwnershipRefusesSymlinkedSocketPath(t *testing.T) {
	base := shortTempDir(t)
	secureDir := filepath.Join(base, "secure")
	if err := os.Mkdir(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	realSock := filepath.Join(secureDir, "real.sock")
	listener, err := net.Listen("unix", realSock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	linkSock := filepath.Join(secureDir, "control.sock")
	if err := os.Symlink(realSock, linkSock); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	err = checkSocketOwnership(linkSock)
	if err == nil {
		t.Fatal("expected refusal for a symlinked socket path")
	}
	// errors.Is against the exact sentinel: this must be errSocketSymlink specifically
	// (the socket path itself is the symlink), not merely any error whose text happens
	// to contain "symlink" — a substring match would still pass if this check were
	// swapped with, say, errSocketMissingStickyBit's ancestor-walk symlink refusal.
	if !errors.Is(err, errSocketSymlink) {
		t.Errorf("expected errors.Is(err, errSocketSymlink), got: %v", err)
	}
}

// TestCheckSocketOwnershipRefusesSymlinkedIntermediateDirectory covers an intermediate
// directory in the walk being a symlink owned by a non-root user (here, whichever user
// runs the test — the same distinction checkSocketOwnershipWalk makes: a symlink that
// an unprivileged local attacker could plausibly have planted must be refused, unlike
// one owned by root, which no unprivileged attacker can replace — see
// TestCheckSocketOwnershipWalkAcceptsRootOwnedSymlinkedAncestor for that accepted case,
// exercised through the injectable lstatFunc since faking a real root-owned test
// directory would need root.
func TestCheckSocketOwnershipRefusesSymlinkedIntermediateDirectory(t *testing.T) {
	base := shortTempDir(t)
	actualDir := filepath.Join(base, "actual")
	if err := os.Mkdir(actualDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkedDir := filepath.Join(base, "linked")
	if err := os.Symlink(actualDir, linkedDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	sockPath := filepath.Join(linkedDir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	err = checkSocketOwnership(sockPath)
	if err == nil {
		t.Fatal("expected refusal when an enclosing directory in the path is a symlink owned by a non-root user")
	}
	// errors.Is against the exact sentinel — see TestCheckSocketOwnershipRefusesSymlinkedSocketPath
	// for why a substring match on "symlink" is not enough.
	if !errors.Is(err, errSocketSymlink) {
		t.Errorf("expected errors.Is(err, errSocketSymlink), got: %v", err)
	}
}

// fakeLstat builds an lstatFunc backed by a fixed map, so a test can drive an exact
// directory shape (ownership, mode, symlink target) without touching the real
// filesystem — needed here since the shape that matters in production (macOS's
// /tmp -> /private/tmp, both root-owned) cannot be reproduced under an unprivileged
// test's own t.TempDir().
func fakeLstat(entries map[string]dirStat) lstatFunc {
	return func(path string) (dirStat, error) {
		info, ok := entries[path]
		if !ok {
			return dirStat{}, fmt.Errorf("fakeLstat: no entry for %s", path)
		}
		return info, nil
	}
}

// TestCheckSocketOwnershipWalkAcceptsRootOwnedSymlinkedAncestor covers refusing every
// symlink unconditionally, including one owned by root — and macOS's /tmp is exactly
// that: a symlink to /private/tmp, owned by root — which would refuse the real daemon
// socket path (/tmp/cc-daemon-<uid>/<id>/control.sock) on every run on that platform.
// This drives the exact shape via the injectable lstatFunc: /tmp (root, a symlink to
// /private/tmp) -> /private/tmp (root, mode 041777, sticky) -> boundary, accepted.
func TestCheckSocketOwnershipWalkAcceptsRootOwnedSymlinkedAncestor(t *testing.T) {
	uid := os.Getuid()
	lstat := fakeLstat(map[string]dirStat{
		"/tmp/cc-daemon-x/fake-session-dir": {uid: uid, mode: 0o700},
		"/tmp/cc-daemon-x":                  {uid: uid, mode: 0o700},
		"/tmp":                              {uid: 0, symlink: true, target: "private/tmp"},
		"/private/tmp":                      {uid: 0, mode: os.ModeSticky | 0o777},
	})

	err := checkSocketOwnershipWalk("/tmp/cc-daemon-x/fake-session-dir/control.sock", "/tmp/cc-daemon-x/fake-session-dir", uid, lstat)
	if err != nil {
		t.Errorf("expected a root-owned symlinked ancestor (/tmp -> /private/tmp) to be accepted, got: %v", err)
	}
}

// TestCheckSocketOwnershipWalkRefusesNonRootOwnedSymlinkedAncestor is the accepted
// case's negative counterpart: the same shape, except /tmp is owned by a non-root uid
// instead of root. An unprivileged local attacker could plant exactly this, so it must
// be refused regardless of what it points to.
func TestCheckSocketOwnershipWalkRefusesNonRootOwnedSymlinkedAncestor(t *testing.T) {
	uid := os.Getuid()
	lstat := fakeLstat(map[string]dirStat{
		"/tmp/cc-daemon-x/fake-session-dir": {uid: uid, mode: 0o700},
		"/tmp/cc-daemon-x":                  {uid: uid, mode: 0o700},
		"/tmp":                              {uid: uid + 1, symlink: true, target: "private/tmp"},
		"/private/tmp":                      {uid: 0, mode: os.ModeSticky | 0o777},
	})

	err := checkSocketOwnershipWalk("/tmp/cc-daemon-x/fake-session-dir/control.sock", "/tmp/cc-daemon-x/fake-session-dir", uid, lstat)
	if err == nil {
		t.Fatal("expected a non-root-owned symlinked ancestor to be refused")
	}
	// errors.Is against the exact sentinel — see TestCheckSocketOwnershipRefusesSymlinkedSocketPath
	// for why a substring match on "symlink" is not enough.
	if !errors.Is(err, errSocketSymlink) {
		t.Errorf("expected errors.Is(err, errSocketSymlink), got: %v", err)
	}
}

// --- Error message cleanliness ---

// TestDaemonErrorEmptyCodeStringIsCleanError covers daemonError directly: a reply
// carrying "code" as an actually-present, empty string (not merely an absent field —
// see TestListSessionsErrorWithoutCodeIsCleanError for that case, which went through
// listSessionsOnce's own separate guard) must still produce a clean "unknown error",
// never "unknown error: " with a dangling colon. This guard now lives inside
// daemonError itself so Ping, sendTextOnce and readAttachHeader — which never had
// listSessionsOnce's separate guard — get it too.
func TestDaemonErrorEmptyCodeStringIsCleanError(t *testing.T) {
	err := daemonError(map[string]interface{}{"code": ""})
	if err.Error() != "unknown error" {
		t.Errorf("expected a clean %q, got %q", "unknown error", err.Error())
	}
}

// TestPingErrorWithoutCodeIsCleanError covers the same guard reached through Ping,
// which had no separate guard of its own before this fix — an "ok":false ping reply
// with no code field used to come back as "unknown error: ".
func TestPingErrorWithoutCodeIsCleanError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveOnce(t, listener, func(_ *testing.T, _ []byte) []byte {
			return []byte(`{"ok":false}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	_, err = client.Ping(context.Background())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine did not complete: the client likely never connected")
	}
	if err == nil {
		t.Fatal("expected an error for an ok:false ping reply with no code, got nil")
	}
	if err.Error() != "unknown error" {
		t.Errorf("expected a clean %q, got %q", "unknown error", err.Error())
	}
}

// TestSendTextEPROTORetryDoesNotDoubleDeliverText covers SendText's "reply" op: the
// whole request (including the text) is sent as one write,
// so the property worth proving is that a request refused with EPROTO is never counted
// as an applied delivery by the daemon — only the retried request, sent with the
// correct proto, is. SendText was at 71.4% coverage with no retry test of its own.
func TestSendTextEPROTORetryDoesNotDoubleDeliverText(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu         sync.Mutex
		deliveries []string
	)
	errChan := make(chan error, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				line, err := reader.ReadString('\n')
				if err != nil {
					errChan <- err
					return
				}
				var m map[string]interface{}
				if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &m); err != nil {
					errChan <- err
					return
				}
				switch {
				case m["op"] == "ping":
					conn.Write([]byte(`{"ok":true,"op":"ping","version":"test","proto":9}` + "\n"))
				case i == 0:
					// First attempt carries the stale proto: refused, never applied.
					conn.Write([]byte(`{"ok":false,"code":"EPROTO"}` + "\n"))
				default:
					// Retry carries the freshly negotiated proto: applied exactly once.
					mu.Lock()
					text, _ := m["text"].(string)
					deliveries = append(deliveries, text)
					mu.Unlock()
					conn.Write([]byte(`{"ok":true,"op":"reply"}` + "\n"))
				}
			}()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 5 // stale cached proto, so the first reply is sent immediately

	err = client.SendText(context.Background(), "session123", "hello world")
	if err != nil {
		t.Fatalf("SendText failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not process all three connections")
	}
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != 1 {
		t.Fatalf("expected exactly one applied delivery of the text, got %d: %v", len(deliveries), deliveries)
	}
	if deliveries[0] != "hello world" {
		t.Errorf("expected delivered text %q, got %q", "hello world", deliveries[0])
	}
}

// --- Item 2: the daemonError code table, and every typed error's Error() string ---

// TestDaemonErrorCodeMapping is the table test covering both daemonError's mapping and
// each mapped type's Error() string in one place. Before this test, ETOOLARGE,
// ENOREPLY, ERESPAWNING, ESTARTING and the default/&ErrUnknown{} branch had no test at
// all, and every Error() method in types.go was at 0% coverage: existing tests only
// ever referenced an error's %v inside a t.Errorf/t.Fatalf that runs only on failure,
// so a passing test run never actually called Error().
func TestDaemonErrorCodeMapping(t *testing.T) {
	cases := []struct {
		code    string
		want    error
		wantMsg string
	}{
		{"EPROTO", &ErrProto{}, "proto mismatch"},
		{"EAUTH", &ErrAuth{}, "authentication failed"},
		{"EPEERUID", &ErrPeeruid{}, "peer uid mismatch"},
		{"ETOOLARGE", &ErrToolarge{}, "request exceeds 1MB"},
		{"ENOJOB", &ErrNojob{}, "no such session"},
		{"ENOREPLY", &ErrNoreply{}, "session is not accepting replies"},
		{"ERESPAWNING", &ErrRespawning{}, "session is respawning"},
		{"ESTARTING", &ErrStarting{}, "daemon is starting"},
		{"EWEIRDUNKNOWNCODE", &ErrUnknown{Code: "EWEIRDUNKNOWNCODE"}, "unknown error: EWEIRDUNKNOWNCODE"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			err := daemonError(map[string]interface{}{"code": tc.code})
			if reflect.TypeOf(err) != reflect.TypeOf(tc.want) {
				t.Fatalf("daemonError(%q): expected type %T, got %T", tc.code, tc.want, err)
			}
			if got := err.Error(); got != tc.wantMsg {
				t.Errorf("daemonError(%q).Error() = %q, want %q", tc.code, got, tc.wantMsg)
			}
		})
	}
}

// TestErrKickedMessages covers both of ErrKicked.Error()'s branches directly: with a
// detail, and with none. Every existing kick test only ever references %v inside a
// conditional t.Fatalf that does not run on a passing test, so Error() itself was
// never actually called.
func TestErrKickedMessages(t *testing.T) {
	withDetail := &ErrKicked{Detail: "another connection attached"}
	if got, want := withDetail.Error(), "attach was kicked: another connection attached"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	noDetail := &ErrKicked{}
	if got, want := noDetail.Error(), "attach was kicked by another connection"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- Item 3: Discover's wiring ---

// TestDiscoverWiresResolveToSocketPathAndSetsInitialPath covers Discover directly —
// the package's only production constructor, called by no test before this one.
// Every existing test builds a Client by hand with New and then pokes discoverable
// and resolve directly, which never exercised Discover's own wiring: that resolve is
// set to SocketPath specifically (not merely "some function with the right
// signature"), that discoverable is set to true, and that the client's initial
// socketPath is whatever SocketPath() actually returned.
//
// This overrides socketGlobBase to a temporary directory rather than depending on a
// live daemon on this machine (see TestSocketPathFindsLiveDaemonSocket, which
// necessarily skips when there is none), so it runs deterministically everywhere,
// including CI.
func TestDiscoverWiresResolveToSocketPathAndSetsInitialPath(t *testing.T) {
	base := shortTempDir(t)
	orig := socketGlobBase
	socketGlobBase = base
	t.Cleanup(func() { socketGlobBase = orig })

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	dir := filepath.Join(base, fmt.Sprintf("cc-daemon-%s", currentUser.Uid), "session1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	client := Discover(func() (string, error) { return "key", nil })
	if !client.discoverable {
		t.Error("expected a Discover-created client to be discoverable")
	}
	if client.resolve == nil {
		t.Fatal("expected resolve to be wired")
	}
	if reflect.ValueOf(client.resolve).Pointer() != reflect.ValueOf(SocketPath).Pointer() {
		t.Error("expected resolve to be wired to SocketPath specifically")
	}
	if got := client.currentSocketPath(); got != sockPath {
		t.Errorf("expected the initial socket path %q, got %q", sockPath, got)
	}
}

// TestDiscoverSucceedsWithNoDaemonPresentThenResolvesOnceOneAppears covers the
// recommendation that Discover could not be constructed before the daemon was up:
// Discover used to call SocketPath() eagerly and fail the whole construction with
// ErrDaemonUnavailable when nothing was listening yet. There is no cmd/ in this
// repository yet, so the first real consumer of this client would hit exactly that
// start-order problem and have no choice but to retry Discover itself in a loop.
//
// This overrides socketGlobBase to an empty temporary directory (no daemon present at
// all), confirms Discover still succeeds with no path resolved, then plants a live
// socket under it and confirms a call through the client resolves and connects
// without needing a new Client to be constructed.
func TestDiscoverSucceedsWithNoDaemonPresentThenResolvesOnceOneAppears(t *testing.T) {
	base := shortTempDir(t)
	orig := socketGlobBase
	socketGlobBase = base
	t.Cleanup(func() { socketGlobBase = orig })

	client := Discover(func() (string, error) { return "key", nil })
	if !client.discoverable {
		t.Error("expected a Discover-created client to be discoverable")
	}
	if got := client.currentSocketPath(); got != "" {
		t.Errorf("expected no socket path resolved yet with no daemon present, got %q", got)
	}

	currentUser, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	dir := filepath.Join(base, fmt.Sprintf("cc-daemon-%s", currentUser.Uid), "session1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sockPath := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				if _, err := reader.ReadString('\n'); err != nil {
					return
				}
				conn.Write([]byte(`{"ok":true,"op":"ping","version":"test","proto":1}` + "\n"))
			}(conn)
		}
	}()

	if _, err := client.Ping(context.Background()); err != nil {
		t.Fatalf("expected Ping to succeed once a daemon appears, got: %v", err)
	}
	if got := client.currentSocketPath(); got != sockPath {
		t.Errorf("expected the newly resolved path to be cached, got %q, want %q", got, sockPath)
	}
}

// --- Item 6: readBoundedLine's 1MB cap ---

// TestReadBoundedLineRefusesOversizedLine covers the cap directly: a line with no
// newline that exceeds maxLineBytes must be refused rather than buffered without
// bound.
func TestReadBoundedLineRefusesOversizedLine(t *testing.T) {
	data := bytes.Repeat([]byte("a"), maxLineBytes+10) // no newline anywhere
	reader := bufio.NewReader(bytes.NewReader(data))

	_, err := readBoundedLine(reader)
	if err == nil {
		t.Fatal("expected an error for a line exceeding maxLineBytes")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected the cap violation to be visible in the error, got: %v", err)
	}
}

// TestReadBoundedLineRejectsLineThatOnlyExceedsTheCapWithItsNewline covers the exact
// boundary: readBoundedLine used to check len(line) > maxLineBytes only after
// appending the byte just read, so a line of exactly maxLineBytes content bytes
// followed by a terminating '\n' returned successfully with a total buffered length
// of maxLineBytes+1 — one byte past the documented cap — because the newline check
// short-circuited before the overflow check ever ran. The cap must be enforced before
// a byte is appended, not after, so the buffer never holds more than maxLineBytes
// bytes even counting the newline that ends it.
func TestReadBoundedLineRejectsLineThatOnlyExceedsTheCapWithItsNewline(t *testing.T) {
	data := append(bytes.Repeat([]byte("a"), maxLineBytes), '\n') // maxLineBytes+1 bytes total
	reader := bufio.NewReader(bytes.NewReader(data))

	_, err := readBoundedLine(reader)
	if err == nil {
		t.Fatal("expected a line whose length, including its newline, exceeds maxLineBytes to be refused")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected the cap violation to be visible in the error, got: %v", err)
	}
}
