package daemon

import "errors"

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
}

// Waiting reports whether the session is waiting for a human answer.
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
// Needs is deliberately excluded from the predicate: it is also non-empty, independent
// of waiting, on a session that is merely rate-limited or needs a login refresh (forms
// like "usage limit reached ...", "rate limited ...", "login required ..."). Treating a
// non-empty Needs as waiting on its own would misreport every one of those as a session
// parked for a human decision, which they are not.
func (s Session) Waiting() bool {
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
