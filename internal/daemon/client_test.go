package daemon

// testdata/list_sessions.json is an anonymised capture of a real `list` reply from a
// live daemon (cwd, name, sessionId, nonce, pid and timestamps replaced; needs/intent/
// detail rewritten to neutral text of the same shape). Three of its records are
// derived from that capture. The fourth (short "e4fa5037": tempo=active, state=blocked,
// needs="") is added by hand, because the live capture used for this fixture did not
// happen to contain that form. It is nonetheless attested: this form was observed live
// on this machine, a session parked for roughly an hour with
// detail="awaiting user decision on a dependency version". Under the current rule
// (docs/protocol/daemon-control-socket.md section 3.1) a bare blocked flag with empty
// needs is Stalled, never Waiting — State and Tempo are set by a mechanism the session
// does not control, so this record cannot be told apart from one merely coordinating
// its own subagents by the flags alone, even though its Detail text reads like a
// person-facing decision. That gap is exactly why the protocol obliges Detail to be
// shown verbatim for a session stalled this way.
//
// The fifth (short "f5ab6148") is also added by hand, to cover the `"dying": true` key
// documented in docs/protocol/daemon-control-socket.md sections 4 and 8: a job being
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
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
// lands in Stalled this way (see docs/protocol/daemon-control-socket.md section 3.1),
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
// SendText, SendKeys and Ping; only ReadScreen derives one) left the connect itself
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
	go func() {
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
			serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
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
				} else { // list response
					return []byte(`{"ok":true,"op":"list","jobs":[]}` + "\n")
				}
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
	go func() {
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

	// Wait a bit for goroutine to finish
	time.Sleep(100 * time.Millisecond)

	// Check for any errors from the goroutine
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":"1"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":0}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":true,"op":"ping","version":"2.1.263","proto":1.9}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	info, err := client.Ping(context.Background())
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	_, err = client.ListSessions(context.Background())
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
	// A correctly owned and moded regular file, not an actual socket: it passes
	// checkSocketOwnership (so this exercises resolveSocketCandidate's *dial*-failure
	// skip specifically, not the earlier ownership-refusal skip an already-removed
	// socket file would hit instead) but fails to dial, reproducing a crashed
	// daemon's stale socket left behind on disk.
	deadSocket := filepath.Join(deadDir, "control.sock")
	if err := os.WriteFile(deadSocket, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
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
// to preserve. The candidate here is a plain regular file, not a socket: it passes
// checkSocketOwnership (correctly owned, tightly moded) but a dial against it fails,
// since nothing is listening there.
func TestResolveSocketCandidateReportsDialFailure(t *testing.T) {
	base := shortTempDir(t)
	secureDir := filepath.Join(base, "secure")
	if err := os.Mkdir(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	notASocket := filepath.Join(secureDir, "control.sock")
	if err := os.WriteFile(notASocket, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := resolveSocketCandidate([]string{notASocket})
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EPROTO","error":"proto mismatch"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	_, err = client.Ping(context.Background())
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
	go func() {
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

	err = client.SendText(context.Background(), "session123", "hello", true)
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
	go func() {
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

// TestNoControlKeyLeakedInError covers the same gap as TestErrorCodeEAUTH's comment
// explains: ListSessions never carries the control key at all (client.proto left at
// zero routes the single served response to the ping ensureProto sends first, so
// "list" itself, which has no auth field regardless, is never even reached), so "the
// error contains no key" was trivially true on that path and would have stayed true
// even if a write path started leaking one. This exercises SendKeys instead — an
// attach carrying "auth" in its request, a distinct credential-bearing path from
// TestErrorCodeEAUTH's SendText/"reply" — with client.proto pre-set so the served
// response answers the attach itself.
func TestNoControlKeyLeakedInError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var capturedReq map[string]interface{}
	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			if err := json.Unmarshal(req, &capturedReq); err != nil {
				t.Errorf("unmarshalling captured request: %v", err)
			}
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "my-secret-key-12345", nil
	})
	client.proto = 1 // skip the ping, so the one served response answers the attach

	err = client.SendKeys(context.Background(), "session123", "x")

	// Sanity check that this test actually exercises a path carrying the credential.
	if auth, _ := capturedReq["auth"].(string); auth != "my-secret-key-12345" {
		t.Fatalf("request never carried the control key (auth=%q); this test no longer exercises an authenticated path", auth)
	}

	// Error message should not contain the key
	if err == nil {
		t.Fatal("expected an error for a refused attach")
	}
	if strings.Contains(err.Error(), "my-secret-key-12345") {
		t.Error("control key leaked into error message")
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
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
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

	err = client.SendText(context.Background(), "session123", "hello world", true)
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
	go func() {
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
	err = client.SendText(context.Background(), "session123", "hello", true)
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if requestReceived {
		t.Error("server should not have received any request")
	}
}

// TestSendKeysKeyFunctionFailure covers the security blocker: SendKeys is a write into
// a live session's PTY, exactly like SendText writing into a session's prompt, and must
// refuse the same way when no control key is available — never falling back to an
// unauthenticated attach that the daemon would let through on its peer-uid check alone.
// This asserts the fake server sees no connection at all, not merely that an error came
// back, so it fails if a future change dials before checking the key.
func TestSendKeysKeyFunctionFailure(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu              sync.Mutex
		requestReceived bool
	)
	go func() {
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

	// SendKeys must return ErrNoControlKey before it ever dials, so by the time this
	// call returns there is nothing left to wait for.
	err = client.SendKeys(context.Background(), "session123", "hello")
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if requestReceived {
		t.Error("server should not have received any connection at all")
	}
}

func TestSendTextSubmitFalse(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu              sync.Mutex
		requestReceived bool
	)
	go func() {
		conn, _ := listener.Accept()
		if conn != nil {
			mu.Lock()
			requestReceived = true
			mu.Unlock()
			conn.Close()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	// SendText returns the unsupported error before it ever dials, so there is
	// nothing left to wait for by the time this call returns.
	err = client.SendText(context.Background(), "session123", "hello", false)
	var submitErr *ErrSubmitNotSupported
	if !errors.As(err, &submitErr) {
		t.Errorf("expected *ErrSubmitNotSupported, got %T: %v", err, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if requestReceived {
		t.Error("server should not have received any request when submit=false")
	}
}

func TestSendTextErrorENOJOB(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"ENOJOB","error":"no such session"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendText(context.Background(), "missing", "text", true)
	var nojobErr *ErrNojob
	if !errors.As(err, &nojobErr) {
		t.Errorf("expected *ErrNojob, got %T: %v", err, err)
	}
}

func TestReadScreenReturnsStreamedBytes(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n') // read the request

		// Send header line
		headerLine := `{"ok":true,"op":"attach","imarkNonce":"nonce","decModes":{},"via":"local","booting":false,"tempo":"idle","state":"working","cached":false,"stale":false,"workerCliVersion":"2.1.263"}` + "\n"
		conn.Write([]byte(headerLine))

		// Send streamed bytes
		conn.Write([]byte("Hello from terminal"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	output, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("ReadScreen failed: %v", err)
	}

	<-done
	expected := "Hello from terminal"
	if output != expected {
		t.Errorf("expected output %q, got %q", expected, output)
	}
}

func TestReadScreenTailBytes(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n') // read the request

		// Send header line
		headerLine := `{"ok":true,"op":"attach"}` + "\n"
		conn.Write([]byte(headerLine))

		// Send streamed bytes
		fullOutput := "0123456789ABCDEFGHIJ"
		conn.Write([]byte(fullOutput))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	output, err := client.ReadScreen(context.Background(), "session123", 5)
	if err != nil {
		t.Fatalf("ReadScreen failed: %v", err)
	}

	<-done
	expected := "FGHIJ"
	if output != expected {
		t.Errorf("expected last 5 bytes %q, got %q", expected, output)
	}
}

func TestAttachOmitsAuthWhenKeyFails(t *testing.T) {
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
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		line, _ := reader.ReadString('\n')
		if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &capturedReq); err != nil {
			errChan <- err
			return
		}

		// Send header
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "", ErrNoControlKey
	})
	client.proto = 1

	_, _ = client.ReadScreen(context.Background(), "session123", 0)

	<-done
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	// Verify auth key is absent from request
	if _, ok := capturedReq["auth"]; ok {
		t.Error("attach request should not have auth field when key function fails")
	}
}

// TestReadScreenNeverSendsAuthEvenWhenKeySucceeds covers the recommendation that
// ReadScreen sent the control key on attach whenever one happened to be available.
// Reading needs no key at all (docs/protocol/daemon-control-socket.md section 3), so
// a stale control.key — wrong, rotated, whatever — would make the daemon reject the
// attach with EAUTH outright and break a read that would otherwise have succeeded with
// no auth field at all. ReadScreen must never send auth, regardless of whether the key
// function succeeds.
func TestReadScreenNeverSendsAuthEvenWhenKeySucceeds(t *testing.T) {
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
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		line, _ := reader.ReadString('\n')
		if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &capturedReq); err != nil {
			errChan <- err
			return
		}

		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "my-control-key", nil
	})
	client.proto = 1

	_, _ = client.ReadScreen(context.Background(), "session123", 0)

	<-done
	select {
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	default:
	}

	if _, ok := capturedReq["auth"]; ok {
		t.Errorf("ReadScreen must never send auth, got %v", capturedReq["auth"])
	}
}

// TestSendKeysWritesBytesToAttach verifies the key bytes are written to the attach
// connection. The fake daemon holds the connection open after capturing them rather
// than closing right away: a real daemon does not close the attach connection as an
// acknowledgement of delivered input (see SendKeys's comment on why there is no such
// acknowledgement in this protocol), so closing immediately here would misrepresent
// the real server and trip SendKeys's post-write close-detection.
func TestSendKeysWritesBytesToAttach(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu           sync.Mutex
		capturedKeys []byte
		capturedReq  map[string]interface{}
	)
	errChan := make(chan error, 1)
	captured := make(chan struct{})
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n') // read the attach request
		if err != nil {
			errChan <- err
			return
		}
		if err := json.Unmarshal([]byte(strings.TrimSuffix(line, "\n")), &capturedReq); err != nil {
			errChan <- err
			return
		}

		// Send header
		if _, err := conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n")); err != nil {
			errChan <- err
			return
		}

		// Read the key bytes
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil && err != io.EOF {
			errChan <- err
			return
		}
		mu.Lock()
		capturedKeys = append(capturedKeys, buf[:n]...)
		mu.Unlock()
		close(captured)

		// Hold the connection open, as a real daemon would, until the test cleans up.
		<-stop
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "hello keys")
	if err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}

	// errChan is raced directly against captured (and the timeout) here, rather than
	// checked separately afterward with a non-blocking default: a goroutine that hits
	// an error returns immediately without ever closing captured, so a separate,
	// sequential check reached only after this select would never run — the select
	// would already have burned the full 2s on the generic "never captured key bytes"
	// timeout message, hiding the actual, more specific cause.
	select {
	case <-captured:
	case err := <-errChan:
		t.Fatalf("error in server: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("server never captured key bytes")
	}

	mu.Lock()
	defer mu.Unlock()

	// The attach request must carry auth, and the geometry it claims to send.
	if auth, _ := capturedReq["auth"].(string); auth != "key" {
		t.Errorf("expected attach to carry auth=%q, got %v", "key", capturedReq["auth"])
	}
	if cols, _ := capturedReq["cols"].(float64); cols != 80 {
		t.Errorf("expected attach to carry cols=80, got %v", capturedReq["cols"])
	}
	if rows, _ := capturedReq["rows"].(float64); rows != 24 {
		t.Errorf("expected attach to carry rows=24, got %v", capturedReq["rows"])
	}

	expected := []byte("hello keys")
	if !bytes.Equal(capturedKeys, expected) {
		t.Errorf("expected keys %v, got %v", expected, capturedKeys)
	}
}

func TestSendKeysAttachRefusedENOJOB(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"ENOJOB","error":"no such session"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "missing", "x")
	var nojobErr *ErrNojob
	if !errors.As(err, &nojobErr) {
		t.Errorf("expected *ErrNojob, got %T: %v", err, err)
	}
}

func TestSendKeysAttachRefusedEAUTH(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "x")
	var authErr *ErrAuth
	if !errors.As(err, &authErr) {
		t.Errorf("expected *ErrAuth, got %T: %v", err, err)
	}
}

func TestReadScreenAttachRefusedENOJOB(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"ENOJOB","error":"no such session"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "missing", 0)
	var nojobErr *ErrNojob
	if !errors.As(err, &nojobErr) {
		t.Errorf("expected *ErrNojob, got %T: %v", err, err)
	}
	if out != "" {
		t.Errorf("expected empty screen on refused attach, got %q", out)
	}
}

func TestReadScreenAttachRefusedEAUTH(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"invalid auth"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	var authErr *ErrAuth
	if !errors.As(err, &authErr) {
		t.Errorf("expected *ErrAuth, got %T: %v", err, err)
	}
	if out != "" {
		t.Errorf("expected empty screen on refused attach, got %q", out)
	}
}

// TestReadScreenEPROTORetryGetsFreshDeadline covers the recommendation that
// ReadScreen's EPROTO retry reused the same, already-mostly-spent context deadline as
// the first attempt: the deadline is derived once, before the first attempt, from
// c.screenDeadline, and reusing that same ctx for the retry leaves it with whatever
// time happened to remain — here, deliberately, almost none. The first connection
// stalls for longer than client.screenDeadline before answering EPROTO, so by the time
// the retry begins, the context ReadScreen derived at the top would already be past
// its deadline. If the retry inherits that same ctx, its own dial fails immediately
// with a context error; ReadScreen must instead give the retry a fresh, full budget of
// its own, exactly as the first attempt got.
func TestReadScreenEPROTORetryGetsFreshDeadline(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	// Three connections land here, in order: the first attempt's attach (answered
	// EPROTO), a re-ping (isProtoErr's invalidateProto forces ensureProto to re-ping
	// before the retry's own attach — this is correct, existing behaviour, not part of
	// what this test is exercising), and the retry's attach.
	go func() {
		for i := 0; i < 3; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			reader := bufio.NewReader(conn)
			line, _ := reader.ReadString('\n')

			switch {
			case strings.Contains(line, `"op":"ping"`):
				// Answer the re-ping immediately; it should not itself consume any
				// meaningful part of whatever budget is in play.
				conn.Write([]byte(`{"ok":true,"op":"ping","version":"test","proto":1}` + "\n"))
				conn.Close()
			case i == 0:
				// First attempt: stall for most of client.screenDeadline before
				// answering EPROTO — long enough to receive the response inside that
				// budget, but leaving only a sliver of it unspent by the time this
				// attempt returns and the retry begins.
				//
				// The margins below (screenDeadline 1200ms, this stall 1000ms, leaving
				// a 200ms sliver, versus the retry's own 500ms stall) are 4x wider than
				// an earlier version of this test (300ms/250ms/50ms/150ms). That
				// version passed forty race runs locally but left only ~50ms of
				// headroom for dial+write+read+scheduler overhead on the first
				// attempt, and only a ~100ms margin between "long enough to exceed a
				// wrongly-reused deadline's leftover budget" and "short enough to fit
				// inside a correctly-fresh one" — tight enough that a loaded CI runner
				// could flake on scheduling jitter alone, independent of any real
				// regression. Widening every absolute value by the same factor keeps
				// the same proportions (so the same bug is still caught) while making
				// each margin large relative to realistic scheduling jitter.
				time.Sleep(1000 * time.Millisecond)
				conn.Write([]byte(`{"ok":false,"code":"EPROTO"}` + "\n"))
				conn.Close()
			default:
				// Retry's attach: stall longer than the sliver left over from a
				// reused deadline (200ms), but well inside a fresh, full
				// screenDeadline (1200ms), before answering with a short,
				// distinctive screen.
				time.Sleep(500 * time.Millisecond)
				conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
				conn.Write([]byte("retry succeeded"))
				conn.Close()
			}
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.screenDeadline = 1200 * time.Millisecond
	client.readIdleTimeout = 30 * time.Millisecond

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("expected the retry to succeed with its own fresh deadline, got error: %v", err)
	}
	if out != "retry succeeded" {
		t.Errorf("expected %q, got %q", "retry succeeded", out)
	}
}

// TestReadScreenContextDeadlineReturnsPartialBuffer covers a session that prints
// continuously (a spinner, say) and so never goes idle. The context deadline is the
// only thing that ends the read, and what was accumulated is a real partial screen,
// not a failure.
func TestReadScreenContextDeadlineReturnsPartialBuffer(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := conn.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	output, err := client.ReadScreen(ctx, "session123", 0)
	if err != nil {
		t.Fatalf("expected nil error for a partial screen on deadline, got %v", err)
	}
	if len(output) == 0 {
		t.Fatal("expected a non-empty partial screen, got empty string")
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false,"code":"EAUTH","error":"auth failed"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "super-secret-key-abc123xyz789", nil
	})
	client.proto = 1

	err = client.SendText(context.Background(), "session123", "text", true)
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			// Reply with ok=true but no jobs field at all - malformed response
			return []byte(`{"ok":true,"op":"list"}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	sessions, err := client.ListSessions(context.Background())
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
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
	if sessions == nil {
		t.Error("expected empty slice for empty jobs array, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// TestReadScreenNoCtxDeadlineChattySession reproduces the hang reported against a
// session that prints continuously (a spinner, say) and so never goes idle, called
// with context.Background() — the plan's own main.go builds exactly that context.
// Before the fix, the "hard ceiling" inside the loop was recomputed every iteration
// from time.Now(), sliding forward forever instead of acting as a ceiling, so
// ctx.Err() was never satisfied and the loop spun until the fake socket's writer
// stopped (never, here).
//
// The ceiling that actually ends the read is client.screenDeadline:
// readScreenWithDeadline always derives a context deadline from it when ctx carries
// none of its own (context.Background(), exactly this test's call), so
// client.defaultDeadline's fallback in setDeadline is never reached from ReadScreen at
// all. A previous version of this test set client.defaultDeadline instead, and its
// failure message named a "1s defaultDeadline ceiling" — that ceiling never governed
// anything on this path, and the test kept passing in ~2s (production's default
// screenDeadline), not the ~1s the message implied, whatever defaultDeadline was set
// to. Setting client.screenDeadline here both fixes that and keeps the test fast.
func TestReadScreenNoCtxDeadlineChattySession(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := conn.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.screenDeadline = 1 * time.Second

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.ReadScreen(context.Background(), "session123", 0)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ReadScreen did not return within 5s despite a 1s screenDeadline ceiling")
	}
}

func TestReadScreenIdleDetection(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n') // read the attach request

		// Send header line
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		// Send some data
		conn.Write([]byte("Initial output"))

		// Hold the connection open without sending more data. ReadScreen should
		// detect idle and return promptly, not wait for the deadline. Hold until
		// the test cleans up, rather than forever, so the goroutine actually exits.
		<-stop
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.readIdleTimeout = 200 * time.Millisecond // Short timeout for testing
	// The ceiling this call would hit if idle detection failed to fire is
	// client.screenDeadline, not client.defaultDeadline: readScreenWithDeadline
	// always derives ReadScreen's context deadline from screenDeadline when ctx (here,
	// context.Background()) carries none of its own, so setDeadline's fallback to
	// defaultDeadline is never reached on this path at all. Setting it here would be a
	// no-op; screenDeadline is the field that actually bounds this call.
	client.screenDeadline = 2 * time.Second

	// This should return quickly (within ~500ms) due to idle detection,
	// not wait for the full 2-second ceiling
	start := time.Now()
	output, err := client.ReadScreen(context.Background(), "session123", 0)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ReadScreen failed: %v", err)
	}

	expected := "Initial output"
	if output != expected {
		t.Errorf("expected output %q, got %q", expected, output)
	}

	// Verify it returned promptly (should be < 1s with 200ms idle timeout).
	// Without idle detection, it would wait the full 2 seconds.
	if elapsed > 1*time.Second {
		t.Errorf("ReadScreen took too long (%v), indicates idle detection not working", elapsed)
	}
}

// --- New(sock, nil) must not panic (nil key function is substituted with a stub) ---

func TestNewNilKeyFuncReadScreenDoesNotPanic(t *testing.T) {
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
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		<-stop
	}()

	client := New(listener.Addr().String(), nil)
	client.proto = 1
	// No data is ever sent by the fake daemon here, and the idle timer only starts once
	// the first byte arrives — so with nothing arriving at all, the context ceiling
	// (not the idle timeout) is what bounds the wait. That ceiling is
	// client.screenDeadline, not client.defaultDeadline: readScreenWithDeadline always
	// derives ReadScreen's context deadline from screenDeadline when ctx carries none
	// of its own, so defaultDeadline's fallback in setDeadline is never reached on this
	// path. A previous version of this test set defaultDeadline instead, believing it
	// kept the test fast — it did not, and the test actually ran against production's
	// 2s screenDeadline default, not the 200ms named here. Set screenDeadline itself so
	// the comment and the actual runtime agree, and this test stays fast.
	client.screenDeadline = 200 * time.Millisecond

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Errorf("expected ReadScreen to succeed reading without a key, got: %v", err)
	}
	if out != "" {
		t.Errorf("expected empty screen (no data sent), got %q", out)
	}
}

func TestNewNilKeyFuncSendTextReturnsErrNoControlKey(t *testing.T) {
	client := New("/nonexistent/socket/path", nil)
	client.proto = 1

	err := client.SendText(context.Background(), "session123", "hello", true)
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}
}

// TestNewNilKeyFuncSendKeysReturnsErrNoControlKeyAndDialsNothing is the exact
// reproduction of the security blocker: a client built with New(sock, nil) — a
// guaranteed-unavailable key — must not attach with no auth field and let arbitrary
// bytes through on the daemon's peer-uid check alone. It must refuse before dialling at
// all, the same as SendText does.
func TestNewNilKeyFuncSendKeysReturnsErrNoControlKeyAndDialsNothing(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu         sync.Mutex
		sawConnect bool
	)
	go func() {
		conn, _ := listener.Accept()
		if conn != nil {
			mu.Lock()
			sawConnect = true
			mu.Unlock()
			conn.Close()
		}
	}()

	client := New(listener.Addr().String(), nil)
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "rm -rf /\n")
	if !errors.Is(err, ErrNoControlKey) {
		t.Errorf("expected ErrNoControlKey, got %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if sawConnect {
		t.Error("the fake daemon should never have received a connection at all")
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

// --- SendKeys: no per-key acknowledgement exists, but a fast close is surfaced ---

// TestSendKeysConnectionClosingAfterDeliveryIsNotAnError covers the one signal this
// protocol offers past the header — the connection closing right after the keys are
// written — and asserts it is treated as the normal outcome it actually is. The keys
// were already confirmed written (conn.Write succeeded) before the daemon closed the
// connection; closing then happens because the session finished its turn or another
// attacher took over, both of which can happen as a direct consequence of the very keys
// just delivered. Reporting this as an error would invite a caller to retry, and a
// retry here means typing into a live session a second time.
//
// This replaces a previous version of this test, which asserted the opposite (that a
// post-write close must be an error) — enshrining exactly the bug this test now guards
// against.
func TestSendKeysConnectionClosingAfterDeliveryIsNotAnError(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			conn.Close()
			return
		}
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		conn.Close() // simulates a kick or session exit right after delivery
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "x")
	if err != nil {
		t.Fatalf("expected nil: the keys were delivered and the connection closing afterward is normal, got %v", err)
	}
}

// TestSendKeysWriteFailureReturnsErrKeysNotDelivered covers the other, genuinely
// distinct outcome: the write of the key bytes itself fails, before any bytes are
// confirmed sent. Closing the connection immediately after the header — before the
// client's conn.Write call — reliably provokes a write error on the client side. This
// must surface as the typed *ErrKeysNotDelivered, distinguishable from the "delivered,
// then closed" case above, which returns nil.
func TestSendKeysWriteFailureReturnsErrKeysNotDelivered(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			conn.Close()
			return
		}
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		conn.Close() // close before the client ever writes the key bytes
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	// No artificial delay is needed (or useful) here: a previous version of this test
	// slept 50ms right here, before calling SendKeys below — which happens before the
	// dial even starts, let alone before the server has accepted a connection, so it
	// could not have let the close "land" first as intended. The ordering this test
	// actually relies on comes from the protocol shape, not from timing: SendKeys can
	// only reach its write of the key bytes after successfully reading and parsing the
	// header above, which itself requires the server to have already written that
	// header — and the server's very next statement, with no I/O in between, is
	// Close(). That two-instruction gap on the server side is reliably shorter than the
	// client's read-then-parse-then-write round trip, which is why the write below
	// reliably fails without needing a sleep anywhere.
	err = client.SendKeys(context.Background(), "session123", "x")
	var notDelivered *ErrKeysNotDelivered
	if !errors.As(err, &notDelivered) {
		t.Errorf("expected *ErrKeysNotDelivered, got %T: %v", err, err)
	}

	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("server goroutine never finished")
	}
}

// TestErrKeysNotDeliveredNilErrDoesNotPanic covers the recommendation that
// ErrKeysNotDelivered.Error() dereferenced e.Err unconditionally. Every production
// call site constructs it with a non-nil cause (see sendKeysOnce), but the type is
// exported, so any caller outside this package can construct one with Err left nil —
// and doing so must not panic when Error() is called.
func TestErrKeysNotDeliveredNilErrDoesNotPanic(t *testing.T) {
	err := &ErrKeysNotDelivered{}
	got := err.Error() // must not panic
	if got == "" {
		t.Error("expected a non-empty message even with a nil Err")
	}
}

// --- EKICKED: the daemon evicting an attacher must not look like a normal outcome ---

// TestReadScreenDetectsEkicked covers the read path: the daemon writes a plain-text
// "EKICKED: ..." marker into the stream in place of PTY bytes when this attach is
// evicted, then closes. Before the fix, ReadScreen returned those bytes as ordinary
// screen content with a nil error — a poller calling ReadScreen on a cadence would meet
// this regularly and never notice it had been kicked.
func TestReadScreenDetectsEkicked(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		conn.Write([]byte("EKICKED: another connection attached"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("expected *ErrKicked, got %T: %v", err, err)
	}
	if kicked.Detail != "another connection attached" {
		t.Errorf("expected detail %q, got %q", "another connection attached", kicked.Detail)
	}
	if out != "" {
		t.Errorf("expected no screen content on a kicked attach, got %q", out)
	}
}

// TestReadScreenKickedRespectsTailLimit covers a kicked attach returning the whole
// accumulated prefix unbounded, with tail applied only on the non-kicked path below
// it. A caller that asked for the last few bytes could receive up to the full
// maxAttachBytes (1 MB) on this path — a path the protocol document calls a normal
// event, not a rare edge case.
func TestReadScreenKickedRespectsTailLimit(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		conn.Write([]byte("0123456789ABCDEFGHIJ"))
		conn.Write([]byte("EKICKED: another connection attached"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "session123", 5)
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("expected *ErrKicked, got %T: %v", err, err)
	}
	if kicked.Detail != "another connection attached" {
		t.Errorf("expected detail %q, got %q", "another connection attached", kicked.Detail)
	}
	if out != "FGHIJ" {
		t.Errorf("expected the kicked prefix trimmed to the last 5 bytes %q, got %q", "FGHIJ", out)
	}
}

// TestReadScreenMidScreenEkickedTextIsNotAKick covers the blocker that the kick marker
// was matched anywhere in the accumulated screen, so a screen that merely displays the
// text "EKICKED:" reported a kick. This is self-referential: fleetdeck reads the screens
// of Claude Code sessions, and a session working on fleetdeck itself displays
// docs/protocol/daemon-control-socket.md, where "EKICKED:" appears four times.
//
// The fake daemon here sends ordinary screen content containing a line that opens with
// "EKICKED: example" in the middle of the output, then holds the connection open (as a
// live, polled session's attach connection normally stays open — the daemon only closes
// it on an actual kick or session exit). ReadScreen must return the full text with a nil
// error: the marker is never a kick unless the daemon actually closes the connection
// right after writing it.
func TestReadScreenMidScreenEkickedTextIsNotAKick(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		conn.Write([]byte("some normal output\nEKICKED: example\nmore normal output after it"))

		// Hold the connection open, as a real daemon does for a live, polled session
		// that was never kicked — the connection only closes on an actual kick or
		// session exit, neither of which happened here.
		<-stop
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.readIdleTimeout = 100 * time.Millisecond
	// Idle detection is what actually ends this read (the fake daemon writes once and
	// then falls silent); client.screenDeadline is the ceiling that would apply if it
	// didn't, and is set here rather than client.defaultDeadline, which ReadScreen
	// never consults at all (see readScreenWithDeadline).
	client.screenDeadline = 2 * time.Second

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("expected nil error for a screen merely displaying the marker text, got %v", err)
	}
	expected := "some normal output\nEKICKED: example\nmore normal output after it"
	if out != expected {
		t.Errorf("expected full screen text %q, got %q", expected, out)
	}
}

// TestReadScreenSlowFirstPaintReturnsData covers the blocker that ReadScreen returned
// ("", nil) when the daemon was slow to paint: lastReadTime used to be set when the loop
// was entered, so 300ms of silence after the header — a loaded machine, a large screen
// buffer — looked identical to an honestly empty terminal. The idle timer must start only
// after the first successful read, letting the context ceiling bound the wait for the
// first byte.
func TestReadScreenSlowFirstPaintReturnsData(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		time.Sleep(500 * time.Millisecond)
		conn.Write([]byte("hello"))

		<-stop
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.readIdleTimeout = 300 * time.Millisecond
	// The first byte's wait is bounded by the context deadline readScreenWithDeadline
	// derives from client.screenDeadline (ReadScreen never consults defaultDeadline at
	// all), and it must exceed the fake daemon's 500ms sleep above for the read to
	// succeed rather than time out before the first byte arrives.
	client.screenDeadline = 2 * time.Second

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("ReadScreen failed: %v", err)
	}
	if out != "hello" {
		t.Errorf("expected %q, got %q", "hello", out)
	}
}

// TestSendKeysDetectsEkickedAcrossReads covers the blocker that sendKeysOnce's kick
// detection was weaker than ReadScreen's: it read once into a fixed 256-byte buffer and
// checked only that one chunk, so a marker split across two reads was missed. Here the
// fake daemon writes the marker in two separate Write calls with a short delay between
// them, forcing two client-side reads.
func TestSendKeysDetectsEkickedAcrossReads(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		buf := make([]byte, 1024)
		_, _ = conn.Read(buf) // read the key bytes

		conn.Write([]byte("EKI"))
		time.Sleep(20 * time.Millisecond)
		conn.Write([]byte("CKED: split across two reads"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.readIdleTimeout = 300 * time.Millisecond

	err = client.SendKeys(context.Background(), "session123", "x")
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("expected *ErrKicked, got %T: %v", err, err)
	}
	if kicked.Detail != "split across two reads" {
		t.Errorf("expected detail %q, got %q", "split across two reads", kicked.Detail)
	}
}

// TestSendKeysDetectsEkicked covers the key-delivery path: the keys are written, but
// the daemon's very next bytes are the EKICKED marker rather than silence or a close.
// Before the fix, sendKeysOnce read those bytes into its buffer, saw a nil error (data
// was read, not a close), and reported success — indistinguishable from an ordinary
// delivery.
func TestSendKeysDetectsEkicked(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		buf := make([]byte, 1024)
		_, _ = conn.Read(buf) // read the key bytes

		conn.Write([]byte("EKICKED: evicted by another attacher"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "x")
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("expected *ErrKicked, got %T: %v", err, err)
	}
	if kicked.Detail != "evicted by another attacher" {
		t.Errorf("expected detail %q, got %q", "evicted by another attacher", kicked.Detail)
	}
}

// TestReadScreenKickMidStreamNoTrailingNewlineIsDetected covers findKickOpener
// accepting the marker only at offset 0 or immediately after a '\n': a terminal screen
// almost never ends with a trailing newline — the daemon writes the marker "in place
// of" PTY bytes, flush against whatever was already sent, mid-line — so that anchor
// would never fire in the realistic case. This drives exactly that shape: a screen
// with no trailing newline, then the marker, then close.
func TestReadScreenKickMidStreamNoTrailingNewlineIsDetected(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		// No trailing newline before the marker — it lands flush against the screen
		// bytes, exactly as the daemon writes it in place of PTY output.
		conn.Write([]byte("\x1b[2J\x1b[H> waiting for input"))
		conn.Write([]byte("EKICKED: another connection attached"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("expected *ErrKicked, got %T: %v (screen text %q)", err, err, out)
	}
	if kicked.Detail != "another connection attached" {
		t.Errorf("expected detail %q, got %q", "another connection attached", kicked.Detail)
	}
	// Recommendation: a real kick is a normal event (someone attached by hand) and must
	// not throw away the screen accumulated before it — the caller gets the prefix up to
	// the marker alongside the typed error, instead of an empty string.
	wantPrefix := "\x1b[2J\x1b[H> waiting for input"
	if out != wantPrefix {
		t.Errorf("expected the accumulated screen prefix %q on a kicked attach, got %q", wantPrefix, out)
	}
}

// TestSendKeysKickMidStreamNoTrailingNewlineIsDetected is
// TestReadScreenKickMidStreamNoTrailingNewlineIsDetected's SendKeys counterpart: the
// daemon's very next bytes after the key write are ordinary screen content with no
// trailing newline, immediately followed by the kick marker and a close.
func TestSendKeysKickMidStreamNoTrailingNewlineIsDetected(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		buf := make([]byte, 1024)
		_, _ = conn.Read(buf) // read the key bytes

		conn.Write([]byte("\x1b[2J\x1b[H> waiting for input"))
		conn.Write([]byte("EKICKED: another connection attached"))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	err = client.SendKeys(context.Background(), "session123", "x")
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("expected *ErrKicked, got %T: %v", err, err)
	}
	if kicked.Detail != "another connection attached" {
		t.Errorf("expected detail %q, got %q", "another connection attached", kicked.Detail)
	}
}

// TestReadScreenProductionDefaultsChattySessionReturnsPromptly covers ReadScreen
// against a session that redraws continuously, so the idle timeout never fires: it
// must return at its own screenDeadline ceiling (2s in production), not defaultDeadline
// (30s), since readScreenWithDeadline derives its context deadline from screenDeadline,
// never from defaultDeadline.
//
// The previous version of this test asserted `elapsed >= client.defaultDeadline`
// (30s) as its failure condition, while a 10s watchdog (see the select below) fires
// first — that assertion could never be reached, so the test actually proved nothing
// past "returns within 10s". Raising screenDeadline to 9s — nowhere near the 30s it
// was compared against — passed the old assertion outright. This version asserts
// against screenDeadline itself, the value that actually governs, so that regression
// is caught.
//
// Neither defaultDeadline nor screenDeadline is overridden here — the point is to
// measure the actual production ceiling, not one shortened by the test.
func TestReadScreenProductionDefaultsChattySessionReturnsPromptly(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := conn.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	// Deliberately not touching client.defaultDeadline or client.screenDeadline: this
	// test measures the production ceiling.

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.ReadScreen(context.Background(), "session123", 0)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ReadScreen did not return within 10s against a chatty redrawing session")
	}
	elapsed := time.Since(start)

	// The actual ceiling is client.screenDeadline (2s in production, per New's
	// default), not defaultDeadline (30s). wantCeiling is a literal, not
	// client.screenDeadline read back: this test deliberately does not touch
	// client.screenDeadline (see the comment above), so reading the field back would
	// just restate whatever New() happens to set it to and could never catch a
	// regression to that default. The margin is a proportion of wantCeiling (50%) for
	// scheduling slack, rather than a fixed duration that would either be too tight
	// on a loaded CI runner or too loose to catch a regression.
	const wantCeiling = 2 * time.Second
	if client.screenDeadline != wantCeiling {
		t.Fatalf("New()'s default screenDeadline changed to %v; update wantCeiling in this test to match the new production default", client.screenDeadline)
	}
	margin := wantCeiling / 2
	if elapsed >= wantCeiling+margin {
		t.Errorf("ReadScreen took %v, expected it to return at its screenDeadline ceiling (%v, +%v margin)", elapsed, wantCeiling, margin)
	}
}

// TestSendKeysProductionDefaultsChattySessionReturnsPromptly is
// TestReadScreenProductionDefaultsChattySessionReturnsPromptly's counterpart for
// SendKeys, covering the blocker that SendKeys had no ceiling of its own at all: unlike
// ReadScreen, it never derives a context deadline when the caller's (context.Background,
// the documented call path) carries none, and collectUntilIdleOrClosed's per-iteration
// read deadline is recomputed from lastReadTime on every byte received — so a session
// that keeps printing more often than the idle timeout never lets the loop's idle branch
// fire, and with no context deadline in play either, nothing ever ends the call. This
// server sends one byte every 100ms, forever, to reproduce exactly that: SendKeys
// reuses client.screenDeadline (2s in production, see sendKeysOnce) as its own until
// ceiling, not defaultDeadline (30s), so this asserts against screenDeadline for the
// same reason TestReadScreenProductionDefaultsChattySessionReturnsPromptly does — the
// previous `elapsed >= client.defaultDeadline` assertion was unreachable behind this
// test's 10s watchdog and stayed green even with screenDeadline raised to 9s.
func TestSendKeysProductionDefaultsChattySessionReturnsPromptly(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))

		buf := make([]byte, 1024)
		_, _ = conn.Read(buf) // read the key bytes

		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := conn.Write([]byte("x")); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	// Deliberately not touching client.defaultDeadline or client.screenDeadline: this
	// test measures the production ceiling.

	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.SendKeys(context.Background(), "session123", "\r")
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SendKeys did not return within 10s against a session that keeps printing")
	}
	elapsed := time.Since(start)

	// The actual ceiling is client.screenDeadline (2s in production), not
	// defaultDeadline: see TestReadScreenProductionDefaultsChattySessionReturnsPromptly's
	// comment for why wantCeiling is a literal rather than client.screenDeadline read
	// back, and why the margin is proportional rather than a fixed duration.
	const wantCeiling = 2 * time.Second
	if client.screenDeadline != wantCeiling {
		t.Fatalf("New()'s default screenDeadline changed to %v; update wantCeiling in this test to match the new production default", client.screenDeadline)
	}
	margin := wantCeiling / 2
	if elapsed >= wantCeiling+margin {
		t.Errorf("SendKeys took %v, expected it to return at its screenDeadline ceiling (%v, +%v margin)", elapsed, wantCeiling, margin)
	}
}

// TestCollectUntilIdleOrClosedBoundsFirstByteWaitWithNoContextDeadline covers the
// "bounded wait" branch with idleFromStart == false and a context carrying no
// deadline: recomputing time.Now().Add(idleTimeout) on every loop iteration would push
// the deadline forward by another idleTimeout each time a read timed out, so
// gotFirstByte never becomes true, ctx.Err() never fires (context.Background() never
// errors), and the loop never returns. This exercises collectUntilIdleOrClosed
// directly (it is a package-level function with two callers, not reachable this way
// through either ReadScreen or SendKeys today) against a connection that sends nothing
// at all, and asserts it returns well within a bounded window instead of hanging.
func TestCollectUntilIdleOrClosedBoundsFirstByteWaitWithNoContextDeadline(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	t.Cleanup(func() { _ = clientConn.Close() })

	idleTimeout := 100 * time.Millisecond

	done := make(chan struct {
		data   []byte
		closed bool
	}, 1)
	go func() {
		data, closed := collectUntilIdleOrClosed(context.Background(), clientConn, bufio.NewReader(clientConn), idleTimeout, maxAttachBytes, false, time.Time{})
		done <- struct {
			data   []byte
			closed bool
		}{data, closed}
	}()

	select {
	case result := <-done:
		if result.closed {
			t.Errorf("expected closed=false for a connection that never closed, got true")
		}
		if len(result.data) != 0 {
			t.Errorf("expected no data from a silent connection, got %q", result.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collectUntilIdleOrClosed did not return within 2s despite a 100ms idle timeout and no context deadline — it is looping forever")
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
	if !strings.Contains(cause.Error(), "group or other") {
		t.Errorf("expected the wrapped cause to name the actual reason, got %q", cause.Error())
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
	real := filepath.Join(tmpDir, "real.key")
	if err := os.WriteFile(real, []byte("deadbeefdeadbeefdeadbeefdeadbeef"), 0o600); err != nil {
		t.Fatalf("write real key file: %v", err)
	}
	keyPath := filepath.Join(dir, "control.key")
	if err := os.Symlink(real, keyPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err := ControlKey()
	if !errors.Is(err, ErrNoControlKey) {
		t.Fatalf("expected ErrNoControlKey for a symlinked key file, got %v", err)
	}
	cause := errors.Unwrap(err)
	if cause == nil || !strings.Contains(cause.Error(), "symlink") {
		t.Errorf("expected the wrapped cause to name the symlink explicitly, got %v", cause)
	}
}

// --- Kick detection must be a compound signal, not "the marker's last occurrence
// plus a close" ---

// TestReadScreenGrepDisplayingMarkerThenExitIsNotAKick reproduces a session that
// greps this very repository for the marker text (so the
// literal string "EKICKED:" appears twice — once in the grep invocation, once in its
// match line), then exits normally. bytes.LastIndex plus "the connection closed" fired
// on this every time: it only required the marker to be the last *occurrence*, not the
// last *thing sent*. The text after the last occurrence here is " marker\n$ exit\n" — a
// newline right there, followed by more screen content — which a real
// "EKICKED: <reason>" is never followed by, since the daemon closes immediately after
// writing the reason.
func TestReadScreenGrepDisplayingMarkerThenExitIsNotAKick(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		conn.Write([]byte("$ grep -n EKICKED: docs/protocol/daemon-control-socket.md\n42: EKICKED: marker\n$ exit\n"))
		// The session exited and the daemon closed the connection right after writing
		// this — the close is real, but it must not be reported as a kick.
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("expected nil error for a screen that merely displayed the marker text before exiting, got %v", err)
	}
	want := "$ grep -n EKICKED: docs/protocol/daemon-control-socket.md\n42: EKICKED: marker\n$ exit\n"
	if out != want {
		t.Errorf("expected the complete screen text %q, got %q", want, out)
	}
}

// TestReadScreenLongMultilineTailAfterMarkerIsNotAKick covers the marker appearing but
// what follows it being long and containing a newline — the shape of ordinary screen
// content, not the daemon's own short, single-line reason. A real "EKICKED: <reason>"
// is always the very last thing the daemon sends before closing the connection;
// nothing this verbose ever follows it.
func TestReadScreenLongMultilineTailAfterMarkerIsNotAKick(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	tail := strings.Repeat("a", 10) + "\n" + strings.Repeat("b", 290)
	screen := "EKICKED: " + tail

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		conn.Write([]byte(screen))
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("expected nil error for a marker followed by a long, multi-line tail, got %v", err)
	}
	if out != screen {
		t.Errorf("expected the complete screen text, got %q", out)
	}
}

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
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected the refusal to name the symlink explicitly, got: %v", err)
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
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected the refusal to name the symlink explicitly, got: %v", err)
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
		"/tmp/cc-daemon-x/b9184055": {uid: uid, mode: 0o700},
		"/tmp/cc-daemon-x":          {uid: uid, mode: 0o700},
		"/tmp":                      {uid: 0, symlink: true, target: "private/tmp"},
		"/private/tmp":              {uid: 0, mode: os.ModeSticky | 0o777},
	})

	err := checkSocketOwnershipWalk("/tmp/cc-daemon-x/b9184055/control.sock", "/tmp/cc-daemon-x/b9184055", uid, lstat)
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
		"/tmp/cc-daemon-x/b9184055": {uid: uid, mode: 0o700},
		"/tmp/cc-daemon-x":          {uid: uid, mode: 0o700},
		"/tmp":                      {uid: uid + 1, symlink: true, target: "private/tmp"},
		"/private/tmp":              {uid: 0, mode: os.ModeSticky | 0o777},
	})

	err := checkSocketOwnershipWalk("/tmp/cc-daemon-x/b9184055/control.sock", "/tmp/cc-daemon-x/b9184055", uid, lstat)
	if err == nil {
		t.Fatal("expected a non-root-owned symlinked ancestor to be refused")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected the refusal to name the symlink explicitly, got: %v", err)
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

	go func() {
		serveOnce(t, listener, func(t *testing.T, req []byte) []byte {
			return []byte(`{"ok":false}` + "\n")
		})
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})

	_, err = client.Ping(context.Background())
	if err == nil {
		t.Fatal("expected an error for an ok:false ping reply with no code, got nil")
	}
	if err.Error() != "unknown error" {
		t.Errorf("expected a clean %q, got %q", "unknown error", err.Error())
	}
}

// wrappedTimeoutNetErr is a net.Error whose Timeout() reports true, used below wrapped
// inside another error via fmt.Errorf's %w so it is no longer, itself, the concrete
// type a plain `err.(net.Error)` type assertion would match.
type wrappedTimeoutNetErr struct{}

func (wrappedTimeoutNetErr) Error() string   { return "fake wrapped timeout" }
func (wrappedTimeoutNetErr) Timeout() bool   { return true }
func (wrappedTimeoutNetErr) Temporary() bool { return true }

// alwaysWrappedTimeoutConn is a minimal net.Conn whose Read always fails with a
// timeout error wrapped inside another error, rather than the raw net.Error the
// standard library's own bufio.Reader normally passes through unwrapped. That pass-
// through behaviour is the only reason a plain `err.(net.Error)` type assertion has
// worked so far in collectUntilIdleOrClosed; this fake proves the code is correct even
// when a reader wraps its errors instead.
//
// The embedded net.Conn is a real net.Pipe() endpoint (see newAlwaysWrappedTimeoutConn),
// not a bare, nil interface value: a nil-embedded net.Conn only happens to work here
// because this test's one call path (Read and SetReadDeadline, both overridden below)
// never reaches the embedded value's own methods — but the type is otherwise a
// perfectly ordinary net.Conn, and calling any of its other methods (Close, Write,
// LocalAddr, ...) on a nil embedded value would panic instead of failing cleanly. A
// real pipe endpoint behaves correctly for all of them.
type alwaysWrappedTimeoutConn struct{ net.Conn }

// newAlwaysWrappedTimeoutConn wires alwaysWrappedTimeoutConn to a real net.Pipe()
// endpoint and arranges for both ends to be closed at test cleanup.
func newAlwaysWrappedTimeoutConn(t *testing.T) alwaysWrappedTimeoutConn {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return alwaysWrappedTimeoutConn{Conn: client}
}

func (alwaysWrappedTimeoutConn) Read([]byte) (int, error) {
	time.Sleep(2 * time.Millisecond)
	return 0, fmt.Errorf("read: %w", wrappedTimeoutNetErr{})
}

func (alwaysWrappedTimeoutConn) SetReadDeadline(time.Time) error { return nil }

// TestCollectUntilIdleOrClosedUnwrapsWrappedTimeoutError covers the recommendation:
// collectUntilIdleOrClosed used a plain `err.(net.Error)` type assertion, which only
// worked because bufio.Reader.Read happens to return the transport error unwrapped
// today. A wrapped timeout error must still be recognised as a timeout (and the idle
// window honoured) rather than falling into the "connection closed" branch — which
// would turn an ordinary timeout into a false kick, since detectKick treats
// "connection closed" as one of its three required conditions.
func TestCollectUntilIdleOrClosedUnwrapsWrappedTimeoutError(t *testing.T) {
	conn := newAlwaysWrappedTimeoutConn(t)
	reader := bufio.NewReader(conn)

	data, closed := collectUntilIdleOrClosed(context.Background(), conn, reader, 20*time.Millisecond, 0, true, time.Time{})
	if closed {
		t.Fatal("expected a wrapped timeout error to be recognised as a timeout, not a closed connection")
	}
	if len(data) != 0 {
		t.Errorf("expected no data from a connection that only ever times out, got %q", data)
	}
}

// --- A kick whose EOF arrives after the read ceiling is documented, accepted,
// residual behaviour, not a bug to silently paper over ---

// TestReadScreenMarkerAtEndWithoutObservedCloseIsNotAKick locks in the deliberate
// decision recorded on detectKick and in docs/protocol/daemon-control-socket.md section
// 8: condition 1 (an observed close) is required, so a marker sitting at the very end
// of the buffer when the read ceiling fires — with the connection's EOF not yet
// observed — is reported as ordinary screen content, not ErrKicked. The fake daemon
// here writes the marker and then holds the connection open (never closes it) past a
// deliberately short screenDeadline, reproducing exactly the residual case the
// documentation describes; this is the current, accepted behaviour, and this test
// exists so a future change to that decision is deliberate, not accidental.
func TestReadScreenMarkerAtEndWithoutObservedCloseIsNotAKick(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })

	go func() {
		conn, _ := listener.Accept()
		if conn == nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		_, _ = reader.ReadString('\n')
		conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
		conn.Write([]byte("EKICKED: another connection attached"))

		// Hold the connection open well past the client's short screenDeadline below,
		// so its EOF is never observed within the read: closed stays false.
		<-stop
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 1
	client.readIdleTimeout = 2 * time.Second // long enough that idle never fires first
	client.screenDeadline = 50 * time.Millisecond

	out, err := client.ReadScreen(context.Background(), "session123", 0)
	if err != nil {
		t.Fatalf("expected nil error (the marker is reported as ordinary content, per the documented residual risk), got %v", err)
	}
	if out != "EKICKED: another connection attached" {
		t.Errorf("expected the marker's bytes as ordinary screen content, got %q", out)
	}
}

// --- Item 1: the EPROTO retry safety claim on SendKeys and SendText ---

// TestSendKeysEPROTORetryDoesNotDoubleDeliverKeys is the test that actually verifies
// SendKeys's comment on isProtoErr: "EPROTO always surfaces at (or before) the attach
// header, strictly before any key bytes are written, so retrying here never
// double-delivers keys." Nothing exercised this claim before — SendKeys was at 60%
// coverage and only ListSessions and ReadScreen had retry tests at all.
//
// The fake daemon's first attach answers EPROTO and then actively checks whether any
// key bytes arrive anyway (reading with a short deadline before closing) — if
// sendKeysOnce's ordering were wrong and it wrote the key bytes before checking the
// header's ok field, this would catch that stray write directly, not just the visible
// symptom of the wrong count below. The second attach (after the forced re-ping)
// accepts and captures the keys. Exactly one delivery must be recorded.
func TestSendKeysEPROTORetryDoesNotDoubleDeliverKeys(t *testing.T) {
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	var (
		mu          sync.Mutex
		deliveries  [][]byte
		strayWrites [][]byte
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
				switch {
				case strings.Contains(line, `"op":"ping"`):
					conn.Write([]byte(`{"ok":true,"op":"ping","version":"test","proto":9}` + "\n"))
				case i == 0:
					// First attempt: refuse before ever confirming any key bytes.
					conn.Write([]byte(`{"ok":false,"code":"EPROTO"}` + "\n"))
					// Prove the claim directly: check whether the client wrote
					// anything to this connection despite the refusal. This must
					// read through the same bufio.Reader used above, not
					// conn.Read directly: bufio.Reader.ReadString('\n') can pull
					// more than just the line into its own internal buffer in a
					// single underlying Read — a stray write sent close behind
					// the request line would already be sitting in that buffer,
					// invisible to a fresh conn.Read call that bypasses it.
					_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
					buf := make([]byte, 64)
					if n, _ := reader.Read(buf); n > 0 {
						mu.Lock()
						strayWrites = append(strayWrites, append([]byte{}, buf[:n]...))
						mu.Unlock()
					}
				default:
					// Retry's attach: succeed, then capture the key bytes.
					conn.Write([]byte(`{"ok":true,"op":"attach"}` + "\n"))
					buf := make([]byte, 64)
					n, rerr := reader.Read(buf)
					if rerr != nil && rerr != io.EOF {
						errChan <- rerr
						return
					}
					mu.Lock()
					deliveries = append(deliveries, append([]byte{}, buf[:n]...))
					mu.Unlock()
				}
			}()
		}
	}()

	client := New(listener.Addr().String(), func() (string, error) {
		return "key", nil
	})
	client.proto = 5 // stale cached proto, so the first attach is sent immediately

	err = client.SendKeys(context.Background(), "session123", "hello")
	if err != nil {
		t.Fatalf("SendKeys failed: %v", err)
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
	if len(strayWrites) != 0 {
		t.Fatalf("FALSE CLAIM: key bytes were written to the connection refused with EPROTO, before the retry: %v — the comment on SendKeys's EPROTO retry is wrong", strayWrites)
	}
	if len(deliveries) != 1 {
		t.Fatalf("expected exactly one delivery of the key bytes, got %d: %v", len(deliveries), deliveries)
	}
	if string(deliveries[0]) != "hello" {
		t.Errorf("expected delivered keys %q, got %q", "hello", deliveries[0])
	}
}

// TestSendTextEPROTORetryDoesNotDoubleDeliverText is SendKeys's sibling test for
// SendText's "reply" op: the whole request (including the text) is sent as one write,
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

	err = client.SendText(context.Background(), "session123", "hello world", true)
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

// TestErrSubmitNotSupportedMessage covers ErrSubmitNotSupported.Error(), never called
// directly by any existing test (only errors.As).
func TestErrSubmitNotSupportedMessage(t *testing.T) {
	err := &ErrSubmitNotSupported{}
	want := "the control socket always submits a reply; holding text unsent is not supported"
	if got := err.Error(); got != want {
		t.Errorf("got %q, want %q", got, want)
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

// TestErrKeysNotDeliveredWithCauseMessage covers ErrKeysNotDelivered.Error()'s
// with-cause branch directly. TestErrKeysNotDeliveredNilErrDoesNotPanic already covers
// the nil-cause branch.
func TestErrKeysNotDeliveredWithCauseMessage(t *testing.T) {
	err := &ErrKeysNotDelivered{Err: errors.New("broken pipe")}
	want := "keys not confirmed delivered: broken pipe"
	if got := err.Error(); got != want {
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

	client, err := Discover(func() (string, error) { return "key", nil })
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
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

// --- Item 4: trimToRuneBoundary's actual loop body ---

// TestTrimToRuneBoundaryDropsPartialMultiByteRunePrefix cuts through the middle of a
// multi-byte UTF-8 rune and asserts the broken tail bytes are dropped while the valid
// content after them survives intact. Every existing tail-trimming test used pure
// ASCII, so trimToRuneBoundary's loop body — its only reason to exist — never actually
// executed.
func TestTrimToRuneBoundaryDropsPartialMultiByteRunePrefix(t *testing.T) {
	full := []byte("日hello") // 0xE6 0x97 0xA5 'h' 'e' 'l' 'l' 'o'
	cut := full[1:]          // drop the rune's first byte, landing mid-rune
	got := trimToRuneBoundary(cut)
	if string(got) != "hello" {
		t.Errorf("expected the broken rune's tail bytes dropped and %q preserved, got %q", "hello", got)
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
