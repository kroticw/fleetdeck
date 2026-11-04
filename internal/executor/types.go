package executor

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUnavailable     = errors.New("executor unavailable")
	ErrUnsupported     = errors.New("operation not supported by executor")
	ErrStarting        = errors.New("session is starting")
	ErrConflict        = errors.New("executor operation conflicts with current state")
	ErrDeliveryUnknown = errors.New("message delivery is unknown")
	ErrNotResumable    = errors.New("session cannot be resumed")
)

// Verdict deliberately has an unknown zero value. Absence is not evidence of no.
type Verdict string

const (
	Unknown Verdict = ""
	No      Verdict = "no"
	Yes     Verdict = "yes"
)

func (v Verdict) MarshalJSON() ([]byte, error) {
	switch v {
	case No:
		return []byte(`"no"`), nil
	case Yes:
		return []byte(`"yes"`), nil
	default:
		return []byte(`"unknown"`), nil
	}
}

// Metric records what was actually reported and when, without inventing zeroes.
type Metric[T any] struct {
	Value      T         `json:"value"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observedAt"`
}

type Capabilities struct {
	Terminal  bool `json:"terminal"`
	Input     bool `json:"input"`
	Send      bool `json:"send"`
	Interrupt bool `json:"interrupt"`
	Stop      bool `json:"stop"`
	Resume    bool `json:"resume"`
	History   bool `json:"history"`
	Context   bool `json:"context"`
	Limits    bool `json:"limits"`
}

// Session is an executor observation. NativeID is never used to route a request.
type Session struct {
	Key          Key              `json:"key"`
	Executor     ID               `json:"executor"`
	NativeID     string           `json:"nativeId"`
	Name         string           `json:"name"`
	CWD          string           `json:"cwd"`
	Role         string           `json:"role,omitempty"`
	PID          int              `json:"pid,omitempty"`
	Lifecycle    string           `json:"lifecycle"`
	Activity     string           `json:"activity"`
	NeedsHuman   Verdict          `json:"needsHuman"`
	Reason       string           `json:"reason,omitempty"`
	ObservedAt   time.Time        `json:"observedAt"`
	Stale        bool             `json:"stale"`
	Capabilities Capabilities     `json:"capabilities"`
	Model        *Metric[string]  `json:"model,omitempty"`
	Context      *Metric[float64] `json:"context,omitempty"`
	Cost         *Metric[float64] `json:"cost,omitempty"`
	Limits       *Metric[[]Limit] `json:"limits,omitempty"`
}

type Limit struct {
	Name        string     `json:"name"`
	UsedPercent float64    `json:"usedPercent"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
}

type StartRequest struct {
	OperationID string
	Board       string
	Card        string
	CWD         string
	Name        string
}

type Message struct{ OperationID, Text string }
type DeliveryState string

const (
	Queued          DeliveryState = "queued"
	Accepted        DeliveryState = "accepted"
	Failed          DeliveryState = "failed"
	DeliveryUnknown DeliveryState = "unknown"
)

type Receipt struct {
	OperationID     string        `json:"operationId"`
	State           DeliveryState `json:"state"`
	NativeMessageID string        `json:"nativeMessageId,omitempty"`
}

type Entry struct {
	ID   string     `json:"id"`
	Role string     `json:"role"`
	Text string     `json:"text"`
	At   *time.Time `json:"at,omitempty"`
}

type Said struct {
	Words         string
	Transcript    string
	Name          string
	NoFinalAnswer bool
}

// Terminal is a held view, not the owner of the process. Close detaches the view.
type Terminal interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(context.Context, int, int) error
	Writable() bool
	Close() error
}
