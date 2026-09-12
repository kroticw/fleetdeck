package daemon

import (
	"errors"
	"strings"
)

var (
	ErrDaemonUnavailable = errors.New("daemon unavailable")
	ErrNoControlKey      = errors.New("control key unavailable")
)

// Session represents a Claude Code session in the daemon.
type Session struct {
	Short     string `json:"short"`
	Nonce     string `json:"nonce"`
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Attempt   int    `json:"attempt"`
	StartedAt int64  `json:"startedAt"`
	CreatedAt int64  `json:"createdAt"`
	CWD       string `json:"cwd"`
	Backend   string `json:"backend"`
	Tempo     string `json:"tempo"`
	State     string `json:"state"`
	Detail    string `json:"detail"`
	Intent    string `json:"intent"`
	Name      string `json:"name"`
	// Agent is the name of the agent *definition* the session runs under -- the
	// value of Claude Code's `--agent` flag ("Agent for the current session.
	// Overrides the 'agent' setting."), naming a role from .claude/agents/, which
	// for this fleet's own sessions is literally "claude". It is not the vendor,
	// not the CLI, and not the model: nothing on this wire names a model at all.
	// The model reaches the panel only through the statusline reporter
	// (cmd/fleetdeck-status), so the daemon does not know it and this field is not
	// a stand-in for it. Reading Agent as "which CLI is this" and drawing an icon
	// from it shows the operator something the field never meant. Empty (absent on
	// the wire) for a session started without --agent, which is the common case;
	// see docs/protocol/daemon-control-socket.md section 4.
	Agent      string `json:"agent"`
	CLIVersion string `json:"cliVersion"`
	Source     string `json:"source"`
	// Needs is the daemon's own words about why the session stopped, and a pointer
	// because absent and empty are different facts. Present and empty means the daemon
	// looked and has no question outstanding; absent means the source never spoke about
	// it at all, and Waiting answers Unknown rather than No. Today's daemon always sends
	// the key (measured on 2.1.269: 7 live records of 7), so nil comes from a source
	// that does not speak this field.
	Needs *string `json:"needs,omitempty"`
	// Dying is true when the job is being killed or retired (see
	// docs/protocol/daemon-control-socket.md section 4). A session's presence in
	// a `list` reply with no `dying` flag is what marks it as alive; without this field
	// a dying session is indistinguishable from a live one at the parse boundary.
	Dying bool `json:"dying"`
}

// stalledNeedsPrefixes is the closed "no person needed" vocabulary: the daemon's own
// non-question needs renderings, matched by prefix, case-sensitively, against the
// daemon's own wording. First extracted from the installed CLI 2.1.263 binary and
// re-checked against 2.1.269, where all seven still render verbatim (see
// docs/protocol/daemon-control-socket.md section 5), it covers the design spec's
// four stall categories:
//
//	limit      -> "usage limit reached"
//	login      -> "login required"
//	API error  -> "API error", "API overloaded", "API unavailable", "invalid API request"
//	rate limit -> "rate limited"
//
// The list is closed deliberately: this vocabulary belongs to the daemon and will
// grow, so a value with an unfamiliar prefix -- including one that looks like a stall
// but isn't listed here -- must fall through to Waiting (the loud counter), never
// Stalled (the quiet one). Calling someone unnecessarily gets noticed and corrected;
// staying silent about a session that actually needs a person never does.
var stalledNeedsPrefixes = []string{
	"usage limit reached",
	"login required",
	"API error",
	"API overloaded",
	"API unavailable",
	"invalid API request",
	"rate limited",
}

// isStalledNeeds reports whether needs matches one of the daemon's closed non-question
// renderings, per stalledNeedsPrefixes.
func isStalledNeeds(needs string) bool {
	for _, prefix := range stalledNeedsPrefixes {
		if strings.HasPrefix(needs, prefix) {
			return true
		}
	}
	return false
}

// Says wraps a daemon utterance for Session.Needs, whose nil means the source never
// spoke rather than spoke and had nothing to report. Constructing the field by hand
// takes a temporary variable and reads like a workaround; this reads like what it is,
// and puts the two cases side by side at the call site: Says("") is a daemon saying
// there is no question, nil is no daemon saying anything.
func Says(needs string) *string {
	return &needs
}

// Verdict is a three-valued answer to a yes-or-no question about a session: yes, no,
// or "the source did not say". The third value is the point of the type.
//
// A plain bool cannot carry it. A source that does not know whether a session is
// waiting for a person has to return something, and with a bool that something is
// false -- indistinguishable, to every caller and to the panel, from a source that
// looked and found the session not waiting. The panel would then not stay quiet: it
// would state, in as many words, that nobody is waiting.
//
// That is the more expensive of the two errors, and the one that never gets corrected.
// Calling someone who did not need calling is noticed within the minute and complained
// about; failing to call someone who was waiting is noticed by nobody, because the
// person waiting does not know they were dropped and the panel looks calm. The whole
// rule set in docs/protocol/daemon-control-socket.md section 5 is built around that
// asymmetry, and a boolean quietly discards it.
//
// Unknown is the zero value on purpose: a Verdict nobody has assigned yet has not been
// told anything, which is exactly what Unknown means.
type Verdict uint8

const (
	// Unknown: the source said nothing this question can be answered from. Not "no".
	Unknown Verdict = iota
	// No: the source spoke, and the answer is no.
	No
	// Yes: the source spoke, and the answer is yes.
	Yes
)

// String makes a Verdict readable in test failures and logs, where "%v" on a bare
// uint8 would print 0, 1, 2 and force the reader to go and look up which is which.
func (v Verdict) String() string {
	switch v {
	case Yes:
		return "yes"
	case No:
		return "no"
	default:
		return "unknown"
	}
}

// Waiting reports whether a person must answer before this session can move: yes, no,
// or unknown when the source never said.
//
// Only the daemon's words (Needs) decide this — never State or Tempo. State and Tempo
// are set by a mechanism the session does not control, so a session waiting on its own
// subagents and one waiting on a person are indistinguishable in those two flags alone;
// only Needs (and, in the flag-only case documented on Stalled, Detail) says in words
// what is actually happening. See docs/protocol/daemon-control-socket.md section 5.
//
// The three cases, and the difference between the last two, which is the whole reason
// this returns a Verdict rather than a bool:
//
//   - Needs absent (nil) — Unknown. The source never mentioned the field, so it has
//     said nothing about whether anyone is waiting. Today's daemon always sends the
//     key, empty string and all (measured against 2.1.269 on a live fleet: 7 records
//     of 7 carried it), so this case does not arise from it at all. It arises from a
//     source that does not speak this field — and answering "no" on its behalf, which
//     is what a bool forces, would have the panel announce that nobody is waiting on
//     the strength of never having asked.
//
//   - Needs present and empty — No. This is a statement, not a silence: the daemon
//     looked and has no question outstanding. With no words there is still nothing to
//     tell "waiting on a person" apart from "waiting on my own subagents", so a bare
//     blocked flag never promotes it (see Stalled, rule 2) -- but the source did answer,
//     and the answer was no.
//
//   - Needs present and non-empty — it decides alone. A value matching
//     stalledNeedsPrefixes (a usage limit, a login prompt, an API error, a rate limit)
//     means the session is Stalled, not Waiting: no answer fixes it, so No. Everything
//     else, including an unfamiliar prefix, is Yes — the closed list is deliberately
//     narrow, so an unrecognised value must land in the counter a person watches.
//
// A Dying session is No, regardless of what Needs says, and regardless of whether Needs
// says anything at all: it is being killed or retired, so no one has to answer it, and
// that is a real answer rather than an absence of one.
func (s Session) Waiting() Verdict {
	if s.Dying {
		return No
	}
	if s.Needs == nil {
		return Unknown
	}
	if *s.Needs == "" {
		return No
	}
	if isStalledNeeds(*s.Needs) {
		return No
	}
	return Yes
}

// Stalled reports whether the session is stopped for a reason no answer will fix, or
// stopped with no words to say why.
//
//  1. Needs non-empty and matching stalledNeedsPrefixes (a usage limit, a login
//     prompt, an API error, a rate limit): stalled, exactly the complement of Waiting's
//     third rule.
//  2. Needs with nothing to say -- empty, or absent altogether -- and State ==
//     "blocked" or Tempo == "blocked": stalled, never Waiting. Per Waiting's own doc
//     comment, a bare flag with no words cannot be told apart from a session waiting on
//     its own subagents, so it is never promoted to the counter a person is expected to
//     act on. It still must not be hidden entirely: docs/protocol/daemon-control-socket.md
//     section 5 requires a session stalled by this rule to be presented with Detail
//     shown verbatim, since Detail is the only field that can still distinguish
//     "awaiting a decision from a person" from "awaiting my own work" once Needs has
//     nothing to say.
//
// This stays a bool while Waiting does not, and that is deliberate rather than an
// oversight. Stalled has the same defect in principle -- a source that cannot say
// reads as "not stalled" -- but it lies far more softly, because a session stalled
// under a source this client cannot read still reaches a person through the silence
// rule in internal/state: a session quiet for longer than the threshold calls someone
// regardless of any flag. That rule is the only one of the three that survives a change
// of source, because it measures time rather than trusting the meaning of a field.
// Making Stalled three-valued is a separate change with its own consequences for the
// quiet counter, and is not made here.
//
// Every session whose Needs says something lands in exactly one of Waiting == Yes or
// Stalled (rule 1 is a full partition of that case); a session whose Needs says nothing
// can only be Stalled (via the flags) or neither, never Waiting == Yes. The UI shows
// Waiting and Stalled as two separate counters, and a session counted in both would
// make the totals lie.
//
// A Dying session is never Stalled, for the same reason Waiting answers No for one: it
// needs no one's attention, not even the kind Stalled reports.
func (s Session) Stalled() bool {
	if s.Dying {
		return false
	}
	if s.Needs != nil && *s.Needs != "" {
		return isStalledNeeds(*s.Needs)
	}
	return s.State == "blocked" || s.Tempo == "blocked"
}

// Info holds daemon version and protocol information.
type Info struct {
	Version string
	Proto   int
}

// Typed error types for daemon response codes
type ErrProto struct{}

func (e *ErrProto) Error() string {
	return "proto mismatch"
}

type ErrAuth struct{}

func (e *ErrAuth) Error() string {
	return "authentication failed"
}

type ErrPeeruid struct{}

func (e *ErrPeeruid) Error() string {
	return "peer uid mismatch"
}

type ErrToolarge struct{}

func (e *ErrToolarge) Error() string {
	return "request exceeds 1MB"
}

type ErrNojob struct{}

func (e *ErrNojob) Error() string {
	return "no such session"
}

type ErrNoreply struct{}

func (e *ErrNoreply) Error() string {
	return "session is not accepting replies"
}

type ErrRespawning struct{}

func (e *ErrRespawning) Error() string {
	return "session is respawning"
}

type ErrStarting struct{}

func (e *ErrStarting) Error() string {
	return "daemon is starting"
}

type ErrUnknown struct {
	Code string
}

func (e *ErrUnknown) Error() string {
	return "unknown error: " + e.Code
}

// ErrKicked indicates that an attach connection was evicted: the daemon writes a
// plain-text "EKICKED: <reason>" marker into the stream instead of PTY bytes and then
// closes the connection, because another attacher took over or the daemon otherwise
// dropped this connection. Detail carries the reason text, if any; it is daemon-supplied
// operational text, never a control key.
type ErrKicked struct {
	Detail string
}

func (e *ErrKicked) Error() string {
	if e.Detail == "" {
		return "attach was kicked by another connection"
	}
	return "attach was kicked: " + e.Detail
}
