package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kroticw/fleetdeck/internal/codex"
	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/executor"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
)

// codexStateDir keeps runtimes beside the configuration that started them,
// so a stand's runtimes never mix with the operator's.
func codexStateDir(configPath string) string {
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}
	return filepath.Join(filepath.Dir(configPath), "runtime")
}

// codexAllowed is whether this panel may start or resume Codex at all: a
// stand without a Codex home of its own would reach the operator's real one.
func codexAllowed(o runOpts, cfg config.Config) bool {
	return cfg.Codex.Enabled && (o.standSocket == "" || cfg.Codex.Home != "")
}

// codexAdapter is this panel's Codex, spawning runtimes from this binary
// with this configuration and the fleet's board.
func codexAdapter(o runOpts, cfg config.Config) (executor.Adapter, error) {
	exe, err := os.Executable()
	if err != nil {
		return executor.Adapter{}, err
	}
	configPath, err := filepath.Abs(o.configPath)
	if err != nil {
		return executor.Adapter{}, err
	}
	board := cfg.BoardPath
	if board != "" {
		if board, err = filepath.Abs(board); err != nil {
			return executor.Adapter{}, err
		}
	}
	return codex.NewAdapter(codex.Runtimes{
		StateDir: codexStateDir(o.configPath),
		Spawn:    codexruntime.StartSpec{Command: []string{exe}, ConfigPath: configPath, Board: board},
	}), nil
}

// executors is the registry every session operation goes through: Claude as
// given, and Codex when this panel may run it. A key names its executor, so a
// Codex key never reaches Claude, nor the other way round.
func executors(o runOpts, cfg config.Config, claude orchestrator.Claude) *executor.Registry {
	adapters := []executor.Adapter{orchestrator.ClaudeAdapter(claude)}
	if a, err := codexAdapter(o, cfg); err == nil {
		// Runtimes already running stay reachable to stop and read, whatever
		// the configuration says now; only starting one needs Codex allowed.
		if !codexAllowed(o, cfg) {
			a.Start, a.Resume = nil, nil
		}
		adapters = append(adapters, a)
	}
	r, err := executor.NewRegistry(adapters...)
	if err != nil {
		// Two fixed, distinct IDs: this cannot fail.
		panic(err)
	}
	return r
}

// isClaude reports whether a session ID is Claude's: every ID that is not a
// Codex key, as it always was.
func isClaude(id string) bool { return !strings.HasPrefix(id, "codex:") }

// startsAny reports whether some executor here can start a session.
func startsAny(r *executor.Registry) bool {
	for _, id := range []executor.ID{executor.Claude, executor.Codex} {
		if a, err := r.ForID(id); err == nil && a.Start != nil && a.Send != nil {
			return true
		}
	}
	return false
}

// stopVia stops a session through its executor, which for Codex proves its
// work stopped first.
func stopVia(r *executor.Registry) func(ctx context.Context, short string) error {
	return func(ctx context.Context, short string) error {
		a, key, err := r.ForKey(short)
		if err != nil {
			return err
		}
		if a.Stop == nil {
			return fmt.Errorf("%w: this panel cannot stop %s", executor.ErrUnsupported, short)
		}
		return a.Stop(ctx, key)
	}
}

// sendVia sends one message through the session's executor, once under a
// fresh operation ID; an unconfirmed delivery is an error, never a resend.
func sendVia(r *executor.Registry) func(ctx context.Context, short, text string) error {
	return func(ctx context.Context, short, text string) error {
		a, key, err := r.ForKey(short)
		if err != nil {
			return err
		}
		if a.Send == nil {
			return fmt.Errorf("%w: this panel cannot send to %s", executor.ErrUnsupported, short)
		}
		receipt, err := a.Send(ctx, key, executor.Message{OperationID: rand.Text(), Text: text})
		switch {
		case err != nil:
			return err
		case receipt.State == executor.Accepted:
			return nil
		case receipt.State == executor.Failed:
			return errors.New("the session rejected the message")
		}
		return executor.ErrDeliveryUnknown
	}
}

// liveRows is a non-Claude executor's live sessions as rows, for a list that
// must see every running session (a cleanup, an ending terminal).
func liveRows(ctx context.Context, a executor.Adapter) []daemon.Session {
	if a.List == nil {
		return nil
	}
	sessions, _ := a.List(ctx)
	var out []daemon.Session
	for _, s := range sessions {
		if row := sessionRow(s); row.lifecycle == state.LifecycleLive {
			out = append(out, row.session)
		}
	}
	return out
}

// sessionRow shows an executor session in the panel's list with only what
// it observed: a question counts as waiting, an unknown state as unknown.
func sessionRow(s executor.Session) listedSession {
	row := daemon.Session{Short: string(s.Key), PID: s.PID, CWD: s.CWD, Name: s.Name}
	if s.Lifecycle != state.LifecycleLive {
		return listedSession{session: row, lifecycle: s.Lifecycle, lastState: s.Activity}
	}
	row.State = map[string]string{"working": "working", "idle": "idle", "error": "blocked"}[s.Activity]
	switch s.NeedsHuman {
	case executor.Yes:
		row.Needs = new(s.Reason)
	case executor.No:
		row.Needs = new("")
	}
	return listedSession{session: row, lifecycle: state.LifecycleLive}
}

// codexSessionRows lists the runtimes kept in stateDir.
func codexSessionRows(ctx context.Context, stateDir string) []listedSession {
	if _, err := os.Stat(stateDir); err != nil {
		return nil
	}
	sessions, err := codex.NewAdapter(codex.Runtimes{StateDir: stateDir}).List(ctx)
	if err != nil {
		return nil
	}
	rows := make([]listedSession, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, sessionRow(s))
	}
	return rows
}

// resumeVia brings a session back through its executor, as the resume route
// expects: a session that cannot come back is ErrSessionNotResumable.
func resumeVia(r *executor.Registry) func(string) error {
	return func(id string) error {
		a, key, err := r.ForKey(id)
		if err != nil || a.Resume == nil {
			return fmt.Errorf("%w: no executor here resumes %s", server.ErrSessionNotResumable, id)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err = a.Resume(ctx, key)
		if errors.Is(err, executor.ErrNotResumable) && !errors.Is(err, server.ErrSessionNotResumable) {
			return fmt.Errorf("%w: %w", server.ErrSessionNotResumable, err)
		}
		return err
	}
}
