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

// Waiting returns true if the session is waiting for user input.
// A session waits when it has tempo="blocked" and has a non-empty needs field.
func (s Session) Waiting() bool {
	return s.Tempo == "blocked" && s.Needs != ""
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
