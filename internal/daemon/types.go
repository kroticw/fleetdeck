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
	Short      string `json:"short"`
	Nonce      string `json:"nonce"`
	SessionID  string `json:"sessionId"`
	PID        int    `json:"pid"`
	Attempt    int    `json:"attempt"`
	StartedAt  int64  `json:"startedAt"`
	CreatedAt  int64  `json:"createdAt"`
	CWD        string `json:"cwd"`
	Backend    string `json:"backend"`
	Tempo      string `json:"tempo"`
	State      string `json:"state"`
	Detail     string `json:"detail"`
	Intent     string `json:"intent"`
	Name       string `json:"name"`
	Agent      string `json:"agent"`
	CLIVersion string `json:"cliVersion"`
	Source     string `json:"source"`
	Needs      string `json:"needs"`
	// Dying is true when the job is being killed or retired (see
	// docs/protocol/daemon-control-socket.md sections 4 and 8). A session's presence in
	// a `list` reply with no `dying` flag is what marks it as alive; without this field
	// a dying session is indistinguishable from a live one at the parse boundary.
	Dying bool `json:"dying"`
}

// questionNeedsPrefixes are the daemon's own renderings of a needs string that pose an
// actual question to a human, as opposed to reporting a stall no answer can fix (a
// usage limit, a login prompt, an API error, a rate limit). The daemon's own text is
// the source of truth for this vocabulary and it may grow, so the comparison lives here
// once rather than being repeated at each call site.
var questionNeedsPrefixes = []string{"answer:", "choose:"}

// isQuestionNeeds reports whether needs is one of the daemon's question renderings.
func isQuestionNeeds(needs string) bool {
	for _, prefix := range questionNeedsPrefixes {
		if strings.HasPrefix(needs, prefix) {
			return true
		}
	}
	return false
}

// Waiting reports whether a person has to answer before this session moves.
//
// Three forms have been observed on a live daemon (see
// docs/protocol/daemon-control-socket.md section 5 for the full account):
//
//	tempo=blocked  state=working  needs="answer: ... (A · B · C)"
//	tempo=active   state=blocked  needs=""    detail="awaiting a decision"
//	tempo=blocked  state=blocked  needs="choose: ..."
//
// Neither State nor Tempo alone accounts for every form — the second form above has
// Tempo == "active" and is still waiting via State; the first and third have a State
// value other than "blocked" and are still waiting via Tempo — so either being
// "blocked" is sufficient and both must be checked.
//
// A third, independent form is a needs string that itself poses a question (its text
// begins with "answer:" or "choose:", per isQuestionNeeds) while neither State nor
// Tempo reports "blocked". This and Stalled are deliberately kept mutually exclusive:
// a needs string that is non-empty but is not a question (a usage limit, a login
// prompt, a rate limit) means the session is Stalled, not Waiting on a person.
//
// A Dying session is never Waiting, regardless of what State, Tempo, or Needs say: it
// is being killed or retired, so no one has to answer it. This overrides every other
// form above, including a session that happens to satisfy all three at once.
func (s Session) Waiting() bool {
	if s.Dying {
		return false
	}
	return s.State == "blocked" || s.Tempo == "blocked" || isQuestionNeeds(s.Needs)
}

// Stalled reports whether the session is stopped for a reason no answer will fix: a
// non-empty Needs that is not one of the question forms Waiting recognises (a usage
// limit, a login prompt, an API error, a rate limit). It is defined as the complement
// of Waiting given a non-empty Needs, so every session lands in exactly one of Waiting
// or Stalled whenever it is stopped at all — the UI shows these as two separate
// counters, and a session counted in both (or neither, while stopped) would make the
// totals lie.
//
// A Dying session is never Stalled either, for the same reason Waiting excludes it: it
// needs no one's attention, not even the kind Stalled reports. Without this explicit
// check, a dying session with a non-empty, non-question Needs would fall straight
// through Waiting's own Dying guard above and still land here.
func (s Session) Stalled() bool {
	if s.Dying {
		return false
	}
	return !s.Waiting() && s.Needs != ""
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

type ErrSubmitNotSupported struct{}

func (e *ErrSubmitNotSupported) Error() string {
	return "the control socket always submits a reply; holding text unsent is not supported"
}

// ErrKeysNotDelivered indicates that SendKeys's write to the attach connection itself
// failed, before the daemon can be assumed to have received the key bytes. Unlike a
// connection that closes normally right after a successful write (which SendKeys treats
// as delivered, per the protocol's own contract — see docs/protocol/daemon-control-socket.md
// section 3), this means delivery could not be confirmed at all.
//
// This must still never be retried blindly: a net.Conn.Write can write a partial prefix
// of its argument before failing, so some of the keys may already have reached the
// daemon even when this error is returned.
type ErrKeysNotDelivered struct {
	Err error
}

func (e *ErrKeysNotDelivered) Error() string {
	if e.Err == nil {
		return "keys not confirmed delivered"
	}
	return "keys not confirmed delivered: " + e.Err.Error()
}

func (e *ErrKeysNotDelivered) Unwrap() error {
	return e.Err
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
