package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/userenv"
)

const (
	daemonStartWait = 15 * time.Second
	daemonStopWait  = 10 * time.Second
)

// ownedDaemon is a Claude Code daemon this panel started because none was
// running. It is the panel's: closing the panel puts it out, with the
// sessions it runs, which stay resumable. A daemon the panel found running is
// never one of these and is never stopped by it.
type ownedDaemon struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// ensureDaemon starts `claude daemon run` when list finds no daemon, and
// waits for it to answer. A stand never starts one: the daemon is found by
// uid, so a stand's would be the operator's. A daemon that does not come up
// is an error; the panel still runs, as it did without a daemon before.
func ensureDaemon(ctx context.Context, o runOpts, agent config.AgentConfig, list func(context.Context) ([]daemon.Session, error)) (*ownedDaemon, error) {
	if o.standSocket != "" {
		return nil, nil
	}
	if _, err := list(ctx); !errors.Is(err, daemon.ErrDaemonUnavailable) {
		return nil, nil
	}
	argv, env := agent.Command, []string(nil)
	if len(argv) == 0 {
		home, _ := os.UserHomeDir()
		bin, err := orchestrator.FindClaude(home, exec.LookPath, claudePlaces)
		if err != nil {
			return nil, err
		}
		argv, env = []string{bin}, claudeEnv(userenv.Environ(), agent.ConfigDir)
	}
	cmd := exec.Command(argv[0], append(argv[1:], "daemon", "run")...)
	cmd.Env = env
	// Its own session: a terminal's signals to the panel's group are not its
	// stop; the panel stops it itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the Claude Code daemon: %w", err)
	}
	d := &ownedDaemon{cmd: cmd, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(d.exited) }()
	deadline := time.After(daemonStartWait)
	for {
		if _, err := list(ctx); err == nil {
			return d, nil
		}
		select {
		case <-d.exited:
			return nil, errors.New("the Claude Code daemon exited as it started")
		case <-deadline:
			d.Stop()
			return nil, fmt.Errorf("the Claude Code daemon did not answer within %s", daemonStartWait)
		case <-ctx.Done():
			d.Stop()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Stop puts out the daemon the panel started. Nil is a daemon it did not
// start, and there is nothing to do.
func (d *ownedDaemon) Stop() {
	if d == nil {
		return
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.exited:
	case <-time.After(daemonStopWait):
		_ = d.cmd.Process.Kill()
		<-d.exited
	}
}

// orchestratorWaker brings a fleet's pinned orchestrator back when the fleet
// is opened and the orchestrator is not running, at most once per every per
// fleet. A resume sends nothing into the session.
type orchestratorWaker struct {
	fleets func() []fleet.Fleet
	listed func(ctx context.Context, id string) (bool, error)
	resume func(id string) error
	every  time.Duration

	mu   sync.Mutex
	last map[string]time.Time
}

func (w *orchestratorWaker) wake(ctx context.Context, name string) error {
	f, err := fleet.Select(w.fleets(), name)
	if err != nil || f.Orchestrator == "" {
		return nil
	}
	w.mu.Lock()
	if w.last == nil {
		w.last = map[string]time.Time{}
	}
	if at, ok := w.last[f.Name]; ok && time.Since(at) < w.every {
		w.mu.Unlock()
		return nil
	}
	w.last[f.Name] = time.Now()
	w.mu.Unlock()
	// Unknown is not asleep: only a session known not to run is woken, and
	// an unknown answer leaves the next opening free to ask again.
	alive, err := w.listed(ctx, f.Orchestrator)
	if err != nil {
		w.mu.Lock()
		delete(w.last, f.Name)
		w.mu.Unlock()
		return nil
	}
	if alive {
		return nil
	}
	if err := w.resume(f.Orchestrator); err != nil {
		return fmt.Errorf("wake the orchestrator %s of fleet %s: %w", f.Orchestrator, f.Name, err)
	}
	log.Printf("fleetdeck: woke the orchestrator %s of fleet %s", f.Orchestrator, f.Name)
	return nil
}
