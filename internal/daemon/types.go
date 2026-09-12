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
	Needs      string `json:"needs"`
	// Dying is true when the job is being killed or retired (see
	// docs/protocol/daemon-control-socket.md section 4). A session's presence in
	// a `list` reply with no `dying` flag is what marks it as alive; without this field
	// a dying session is indistinguishable from a live one at the parse boundary.
	Dying bool `json:"dying"`
}

// stalledNeedsPrefixes is the closed "no person needed" vocabulary: the daemon's own
// non-question needs renderings, matched by prefix, case-sensitively, against the
// daemon's own wording. Extracted from the installed CLI 2.1.263 binary (see
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

// Waiting reports whether a person must answer before this session can move.
//
// Only the daemon's words (Needs) decide this — never State or Tempo. State and Tempo
// are set by a mechanism the session does not control, so a session waiting on its own
// subagents and one waiting on a person are indistinguishable in those two flags alone;
// only Needs (and, in the flag-only case documented on Stalled, Detail) says in words
// what is actually happening. See docs/protocol/daemon-control-socket.md section 5.
//
// Needs empty means never Waiting: with no words from the daemon, there is nothing to
// tell "waiting on a person" apart from "waiting on my own subagents", and guessing the
// former from a bare flag is exactly the ambiguity this rule exists to avoid.
//
// Needs non-empty decides alone: a value matching stalledNeedsPrefixes (a usage limit,
// a login prompt, an API error, a rate limit) means the session is Stalled, not
// Waiting -- no answer fixes it. Everything else, including an unfamiliar prefix,
// means Waiting: the closed list is deliberately narrow, so an unrecognised value must
// land in the counter a person actually watches.
//
// A Dying session is never Waiting, regardless of what Needs says: it is being killed
// or retired, so no one has to answer it.
func (s Session) Waiting() bool {
	if s.Dying {
		return false
	}
	if s.Needs == "" {
		return false
	}
	return !isStalledNeeds(s.Needs)
}

// Stalled reports whether the session is stopped for a reason no answer will fix, or
// stopped with no words to say why.
//
//  1. Needs non-empty and matching stalledNeedsPrefixes (a usage limit, a login
//     prompt, an API error, a rate limit): stalled, exactly the complement of Waiting's
//     first rule.
//  2. Needs empty and State == "blocked" or Tempo == "blocked": stalled, never
//     Waiting -- per Waiting's own doc comment, a bare flag with no words cannot be
//     told apart from a session waiting on its own subagents, so it is never promoted
//     to the counter a person is expected to act on. It still must not be hidden
//     entirely: docs/protocol/daemon-control-socket.md section 5 requires a session
//     stalled by this rule to be presented with Detail shown verbatim, since Detail is
//     the only field that can still distinguish "awaiting a decision from a person"
//     from "awaiting my own work" once Needs has nothing to say.
//
// Every session with Needs non-empty lands in exactly one of Waiting or Stalled (rule 1
// above is a full partition of that case); a session with Needs empty can only be
// Stalled (via the flags) or neither, never Waiting. The UI shows Waiting and Stalled
// as two separate counters, and a session counted in both would make the totals lie.
//
// A Dying session is never Stalled, for the same reason Waiting excludes it: it needs
// no one's attention, not even the kind Stalled reports.
func (s Session) Stalled() bool {
	if s.Dying {
		return false
	}
	if s.Needs != "" {
		return isStalledNeeds(s.Needs)
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
