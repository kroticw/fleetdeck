package executor

import (
	"context"
	"fmt"
)

// Adapter is a table of independently supported operations. A nil function is
// unsupported; consumers receive only the functions their operation needs.
type Adapter struct {
	ID           ID
	Start        func(context.Context, StartRequest) (Session, error)
	List         func(context.Context) ([]Session, error)
	Send         func(context.Context, Key, Message) (Receipt, error)
	Interrupt    func(context.Context, Key) error
	Stop         func(context.Context, Key) error
	Resume       func(context.Context, Key) (Session, error)
	OpenTerminal func(context.Context, Key, int, int) (Terminal, error)
	History      func(context.Context, Key) ([]Entry, error)
	LastWords    func(context.Context, Key) (Said, error)
	// Requests lists what a session waits on and Respond answers one.
	Requests func(context.Context, Key) ([]PendingRequest, error)
	Respond  func(context.Context, Key, Answer) error
}

// Registry is immutable after construction and safe for concurrent lookups.
type Registry struct{ adapters map[ID]Adapter }

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	r := &Registry{adapters: make(map[ID]Adapter, len(adapters))}
	for _, a := range adapters {
		if a.ID != Claude && a.ID != Codex {
			return nil, fmt.Errorf("%w: %q", ErrUnknownExecutor, a.ID)
		}
		if _, exists := r.adapters[a.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate executor %s", ErrConflict, a.ID)
		}
		r.adapters[a.ID] = a
	}
	return r, nil
}

func (r *Registry) ForID(id ID) (Adapter, error) {
	if id != Claude && id != Codex {
		return Adapter{}, ErrUnknownExecutor
	}
	if r != nil {
		if a, ok := r.adapters[id]; ok {
			return a, nil
		}
	}
	return Adapter{}, fmt.Errorf("%w: %s", ErrUnavailable, id)
}

func (r *Registry) ForKey(raw string) (Adapter, Key, error) {
	id, key, err := ParseKey(raw)
	if err != nil {
		return Adapter{}, "", err
	}
	a, err := r.ForID(id)
	return a, key, err
}
