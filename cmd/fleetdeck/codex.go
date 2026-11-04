package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/executor"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
)

// codexNeeds is how a Codex question reaches the waiting counter: words that
// are no stall, so daemon.Session.Waiting reads them as a person needed.
const codexNeeds = "codex: an approval or an answer is pending"

// codexStateDir keeps runtimes beside the configuration that started them,
// so a stand's runtimes never mix with the operator's.
func codexStateDir(configPath string) string {
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	return filepath.Join(filepath.Dir(configPath), "runtime")
}

func isCodexKey(id string) bool { return strings.HasPrefix(id, "codex:") }

// codexSessions starts and tasks one fleet's Codex sessions; nil when Codex
// is off, or on a stand without a Codex home of its own, which would
// otherwise reach the operator's real Codex.
func codexSessions(o runOpts, cfg config.Config) *orchestrator.CodexSessions {
	if !cfg.Codex.Enabled || (o.standSocket != "" && cfg.Codex.Home == "") {
		return nil
	}
	stateDir := codexStateDir(o.configPath)
	return &orchestrator.CodexSessions{
		Start: func(ctx context.Context, cwd, card, _ string) (string, error) {
			spec, err := codexStartSpec(o, stateDir, cfg.BoardPath)
			if err != nil {
				return "", err
			}
			spec.CWD, spec.Card = cwd, card
			key, _, err := codexruntime.Start(ctx, spec)
			return string(key), err
		},
		Send: func(ctx context.Context, key, op, text string) (executor.Receipt, error) {
			return codexSend(ctx, stateDir, key, op, text)
		},
	}
}

func codexSend(ctx context.Context, stateDir, key, op, text string) (executor.Receipt, error) {
	var receipt executor.Receipt
	err := codexCall(ctx, stateDir, key, "send", map[string]string{"operationId": op, "text": text}, &receipt)
	if errors.Is(err, codexruntime.ErrReplyUnknown) || errors.Is(err, executor.ErrDeliveryUnknown) {
		return executor.Receipt{OperationID: op, State: executor.DeliveryUnknown}, nil
	}
	return receipt, err
}

// codexAwareSend routes a message for a Codex key to its runtime, once under
// a fresh operation ID; an unconfirmed delivery is an error, never a resend.
func codexAwareSend(stateDir string, claude func(ctx context.Context, short, text string) error) func(ctx context.Context, short, text string) error {
	return func(ctx context.Context, short, text string) error {
		if !isCodexKey(short) {
			return claude(ctx, short, text)
		}
		receipt, err := codexSend(ctx, stateDir, short, rand.Text(), text)
		switch {
		case err != nil:
			return err
		case receipt.State == executor.Accepted:
			return nil
		case receipt.State == executor.Failed:
			return errors.New("codex rejected the message")
		}
		return executor.ErrDeliveryUnknown
	}
}

// codexAwareStop stops a Codex key through its runtime, which proves its work
// stopped first. Nil stays nil: a panel that stops no sessions stops none.
func codexAwareStop(stateDir string, claude func(ctx context.Context, short string) error) func(ctx context.Context, short string) error {
	if claude == nil {
		return nil
	}
	return func(ctx context.Context, short string) error {
		if isCodexKey(short) {
			return codexCall(ctx, stateDir, short, "stop", nil, nil)
		}
		return claude(ctx, short)
	}
}

// codexResume brings back a Codex key's runtime; its thread is resumed.
func codexResume(o runOpts, board, key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	stateDir := codexStateDir(o.configPath)
	spec, err := codexStartSpec(o, stateDir, board)
	if err != nil {
		return err
	}
	_, err = codexruntime.Resume(ctx, spec, executor.Key(key))
	if errors.Is(err, executor.ErrNotResumable) {
		return fmt.Errorf("%w: %w", server.ErrSessionNotResumable, err)
	}
	return err
}

// codexListed reports whether a Codex runtime answers for key.
func codexListed(ctx context.Context, stateDir, key string) bool {
	_, err := codexruntime.Connect(ctx, stateDir, executor.Key(key))
	return err == nil
}

func codexStartSpec(o runOpts, stateDir, board string) (codexruntime.StartSpec, error) {
	exe, err := os.Executable()
	if err != nil {
		return codexruntime.StartSpec{}, err
	}
	configPath, err := filepath.Abs(o.configPath)
	if err != nil {
		return codexruntime.StartSpec{}, err
	}
	if board, err = filepath.Abs(board); err != nil {
		return codexruntime.StartSpec{}, err
	}
	return codexruntime.StartSpec{StateDir: stateDir, Command: []string{exe}, ConfigPath: configPath, Board: board}, nil
}

// codexRows lists the saved runtimes as panel rows. A record is only what
// was saved; a runtime is asked for its status, and one that cannot answer
// is shown as not known rather than idle.
func codexRows(ctx context.Context, stateDir string) []listedSession {
	if _, err := os.Stat(stateDir); err != nil {
		return nil
	}
	store, err := codexruntime.OpenStore(stateDir)
	if err != nil {
		return nil
	}
	records, err := store.List()
	if err != nil {
		_ = store.Close()
		return nil
	}
	var rows []listedSession
	for _, r := range records {
		var status *codexruntime.Status
		held := true
		if r.Lifecycle == "live" || r.Lifecycle == "starting" || r.Lifecycle == "stopping" {
			askCtx, cancel := context.WithTimeout(ctx, time.Second)
			if c, err := codexruntime.Connect(askCtx, stateDir, r.Key); err == nil {
				var s codexruntime.Status
				if c.Call(askCtx, "status", nil, &s) == nil {
					status = &s
				}
			}
			cancel()
			// A free key lock is a runtime that is gone, whatever its record says.
			if status == nil {
				if owner, err := store.Acquire(r.Key); err == nil {
					held = false
					_ = owner.Close()
				}
			}
		}
		rows = append(rows, codexRow(r, status, held))
	}
	_ = store.Close()
	return rows
}

func codexRow(r codexruntime.Record, status *codexruntime.Status, held bool) listedSession {
	s := daemon.Session{Short: string(r.Key), PID: r.PID, CWD: r.CWD, Name: strings.TrimSuffix(filepath.Base(r.Card), ".md")}
	switch r.Lifecycle {
	case "live", "starting", "stopping":
		if !held {
			return listedSession{session: s, lifecycle: state.LifecycleStopped, lastState: "unknown"}
		}
		if status != nil {
			s.State = map[string]string{"working": "working", "idle": "idle", "error": "blocked"}[status.Activity]
			switch status.NeedsHuman {
			case executor.Yes:
				s.Needs = new(codexNeeds)
			case executor.No:
				s.Needs = new("")
			}
		}
		return listedSession{session: s, lifecycle: state.LifecycleLive}
	case "stopped":
		return listedSession{session: s, lifecycle: state.LifecycleStopped, lastState: "idle"}
	case "dead":
		return listedSession{session: s, lifecycle: state.LifecycleDead}
	}
	// The TUI ended while Codex may still be working: resumable, state unknown.
	return listedSession{session: s, lifecycle: state.LifecycleStopped, lastState: "unknown"}
}

// terminalStream is the part of codexruntime.TerminalStream the bridge uses.
type terminalStream interface {
	Next(deadline time.Time) (codexruntime.TerminalEvent, error)
	Input([]byte) error
	Resize(cols, rows int) error
	Close() error
}

// codexTerminal presents a runtime view as the bridge's Terminal. A repaint
// becomes a terminal reset (ESC c) before the redraw, so the browser's screen
// starts blank exactly where the runtime's replay does.
type codexTerminal struct {
	stream   terminalStream
	pending  []byte
	writable atomic.Bool
}

func (t *codexTerminal) Read(p []byte) (int, error) {
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

func (t *codexTerminal) Write(p []byte) (int, error) {
	if err := t.stream.Input(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *codexTerminal) Resize(_ context.Context, cols, rows int) error {
	return t.stream.Resize(cols, rows)
}
func (t *codexTerminal) Writable() bool { return t.writable.Load() }
func (t *codexTerminal) Close() error   { return t.stream.Close() }

// codexAttach opens a view; the bridge's Close, not its open deadline, ends it.
func codexAttach(ctx context.Context, stateDir, key string, cols, rows int) (*codexTerminal, error) {
	client, err := codexruntime.Connect(ctx, stateDir, executor.Key(key))
	if err != nil {
		return nil, err
	}
	stream, err := client.AttachTerminal(context.WithoutCancel(ctx), nil)
	if err != nil {
		return nil, err
	}
	t := &codexTerminal{stream: stream}
	t.writable.Store(stream.Writable)
	// The geometry belongs to the lease holder; an observer's is ignored.
	if stream.Writable {
		_ = stream.Resize(cols, rows)
	}
	return t, nil
}

// codexCall makes one request to a serving runtime.
func codexCall(ctx context.Context, stateDir, key, method string, params, result any) error {
	client, err := codexruntime.Connect(ctx, stateDir, executor.Key(key))
	if err != nil {
		return err
	}
	return client.Call(ctx, method, params, result)
}
