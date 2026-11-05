package codex

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/executor"
)

// Needs is how a Codex question reaches a waiting counter: words that name
// no stall, so a person is counted as needed.
const Needs = "codex: an approval or an answer is pending"

// Runtimes is where a panel keeps its Codex runtimes and how it spawns one:
// Spawn carries the binary, configuration and board; a request fills in the
// card and the directory.
type Runtimes struct {
	StateDir string
	Spawn    codexruntime.StartSpec
}

// NewAdapter is Codex behind the executor contract. Every operation goes
// through the session's own runtime; none guesses from a TUI screen.
func NewAdapter(rt Runtimes) executor.Adapter {
	call := func(ctx context.Context, key executor.Key, method string, params, result any) error {
		client, err := codexruntime.Connect(ctx, rt.StateDir, key)
		if err != nil {
			return err
		}
		return client.Call(ctx, method, params, result)
	}
	resume := func(ctx context.Context, key executor.Key) (executor.Session, error) {
		client, err := codexruntime.Resume(ctx, rt.spec(""), key)
		if err != nil {
			return executor.Session{Key: key, Executor: executor.Codex}, err
		}
		return liveSession(client.Record), nil
	}
	return executor.Adapter{
		ID: executor.Codex,
		Start: func(ctx context.Context, req executor.StartRequest) (executor.Session, error) {
			spec := rt.spec(req.Board)
			spec.CWD, spec.Card = req.CWD, req.Card
			key, client, err := codexruntime.Start(ctx, spec)
			if err != nil {
				return executor.Session{Key: key, Executor: executor.Codex}, err
			}
			return liveSession(client.Record), nil
		},
		List: func(ctx context.Context) ([]executor.Session, error) { return list(ctx, rt.StateDir) },
		Send: func(ctx context.Context, key executor.Key, m executor.Message) (executor.Receipt, error) {
			var receipt executor.Receipt
			err := call(ctx, key, "send", map[string]string{"operationId": m.OperationID, "text": m.Text}, &receipt)
			// A reply lost after the write may have delivered the task.
			if errors.Is(err, codexruntime.ErrReplyUnknown) || errors.Is(err, executor.ErrDeliveryUnknown) {
				return executor.Receipt{OperationID: m.OperationID, State: executor.DeliveryUnknown}, nil
			}
			return receipt, err
		},
		Interrupt: func(ctx context.Context, key executor.Key) error { return call(ctx, key, "interrupt", nil, nil) },
		Stop:      func(ctx context.Context, key executor.Key) error { return call(ctx, key, "stop", nil, nil) },
		Resume:    resume,
		OpenTerminal: func(ctx context.Context, key executor.Key, cols, rows int) (executor.Terminal, error) {
			client, err := codexruntime.Connect(ctx, rt.StateDir, key)
			if err != nil {
				return nil, err
			}
			// The caller's Close ends the view, not its open deadline.
			stream, err := client.AttachTerminal(context.WithoutCancel(ctx), nil)
			if err != nil {
				return nil, err
			}
			t := &Terminal{stream: stream}
			t.writable.Store(stream.Writable)
			// The geometry belongs to the lease holder; an observer's is ignored.
			if stream.Writable {
				_ = stream.Resize(cols, rows)
			}
			return t, nil
		},
		History: func(ctx context.Context, key executor.Key) ([]executor.Entry, error) {
			var h codexruntime.History
			err := call(ctx, key, "history", map[string]int{"limit": 50}, &h)
			return h.Entries, err
		},
		LastWords: func(ctx context.Context, key executor.Key) (executor.Said, error) {
			var said executor.Said
			err := call(ctx, key, "lastWords", nil, &said)
			if err == nil {
				return said, nil
			}
			// The history outlives the runtime: one that ended is brought
			// back to read it.
			if _, rerr := resume(ctx, key); rerr != nil {
				return said, errors.Join(err, rerr)
			}
			err = call(ctx, key, "lastWords", nil, &said)
			return said, err
		},
		Requests: func(ctx context.Context, key executor.Key) ([]executor.PendingRequest, error) {
			var pending []executor.PendingRequest
			err := call(ctx, key, "requests", nil, &pending)
			return pending, err
		},
		Respond: func(ctx context.Context, key executor.Key, a executor.Answer) error {
			return call(ctx, key, "requests.respond", a, nil)
		},
	}
}

func (rt Runtimes) spec(board string) codexruntime.StartSpec {
	spec := rt.Spawn
	spec.StateDir = rt.StateDir
	if board != "" {
		spec.Board = board
	}
	return spec
}

func liveSession(r codexruntime.Record) executor.Session {
	return executor.Session{Key: r.Key, Executor: executor.Codex, NativeID: r.ThreadID, Name: cardName(r.Card), CWD: r.CWD, PID: r.PID, Lifecycle: "live", Activity: "unknown"}
}

func cardName(card string) string { return strings.TrimSuffix(filepath.Base(card), ".md") }

// list reports the saved runtimes with only what each reports. Runtimes are
// asked in parallel under one deadline, so a wedged one cannot hold up the
// rest.
func list(ctx context.Context, stateDir string) ([]executor.Session, error) {
	store, err := codexruntime.OpenStore(stateDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	records, err := store.List()
	if err != nil {
		return nil, err
	}
	askCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	statuses := make([]*codexruntime.Status, len(records))
	var wg sync.WaitGroup
	for i, r := range records {
		if !serving(r.Lifecycle) {
			continue
		}
		wg.Go(func() {
			client, err := codexruntime.Connect(askCtx, stateDir, r.Key)
			var s codexruntime.Status
			if err == nil && client.Call(askCtx, "status", nil, &s) == nil {
				statuses[i] = &s
			}
		})
	}
	wg.Wait()
	sessions := make([]executor.Session, 0, len(records))
	for i, r := range records {
		held := true
		// A free lock is a runtime that is gone, whatever its record says.
		// Never probed for a starting record: the lock is free between the
		// panel's release and the runtime's own take, and probing then would
		// make the runtime lose its key.
		if statuses[i] == nil && (r.Lifecycle == "live" || r.Lifecycle == "stopping") {
			if owner, err := store.Acquire(r.Key); err == nil {
				held = false
				_ = owner.Close()
			}
		}
		sessions = append(sessions, Observe(r, statuses[i], held))
	}
	return sessions, nil
}

func serving(lifecycle string) bool {
	return lifecycle == "live" || lifecycle == "starting" || lifecycle == "stopping"
}

// Observe is one runtime as a session. Lifecycle is live, stopped or dead;
// for a session that is not live, Activity is the last known state.
func Observe(r codexruntime.Record, status *codexruntime.Status, held bool) executor.Session {
	s := liveSession(r)
	switch {
	case serving(r.Lifecycle) && !held:
		s.Lifecycle = "stopped"
	case serving(r.Lifecycle):
		if status != nil {
			s.Activity, s.NeedsHuman, s.ObservedAt = status.Activity, status.NeedsHuman, status.ObservedAt
			if s.NeedsHuman == executor.Yes {
				s.Reason = Needs
			}
		}
	case r.Lifecycle == "stopped":
		s.Lifecycle, s.Activity = "stopped", "idle"
	case r.Lifecycle == "dead":
		s.Lifecycle, s.Activity = "dead", ""
	default:
		// The TUI ended while Codex may still be working.
		s.Lifecycle = "stopped"
	}
	return s
}

// terminalStream is the part of codexruntime.TerminalStream a view uses.
type terminalStream interface {
	Next(deadline time.Time) (codexruntime.TerminalEvent, error)
	Input([]byte) error
	Resize(cols, rows int) error
	Close() error
}

// Terminal presents a runtime view as an executor.Terminal. A repaint
// becomes a terminal reset (ESC c) before the redraw, so the browser's screen
// starts blank exactly where the runtime's replay does.
type Terminal struct {
	stream   terminalStream
	pending  []byte
	writable atomic.Bool
}

func (t *Terminal) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		ev, err := t.stream.Next(time.Time{})
		if err != nil {
			return 0, err
		}
		if ev.Writable != nil {
			t.writable.Store(*ev.Writable)
		}
		if ev.End != "" {
			return 0, io.EOF
		}
		if ev.Reset {
			t.pending = append(t.pending, "\x1bc"...)
		}
		t.pending = append(t.pending, ev.Data...)
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *Terminal) Write(p []byte) (int, error) {
	if err := t.stream.Input(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *Terminal) Resize(_ context.Context, cols, rows int) error {
	return t.stream.Resize(cols, rows)
}
func (t *Terminal) Writable() bool { return t.writable.Load() }
func (t *Terminal) Close() error   { return t.stream.Close() }
