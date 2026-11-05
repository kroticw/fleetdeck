package codexruntime

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

const defaultStartWait = 30 * time.Second

// ErrRuntimeEnded means the spawned runtime finished without becoming live;
// its record keeps the final lifecycle.
var ErrRuntimeEnded = errors.New("codex runtime ended before it became live")

// StartSpec describes how a panel spawns a runtime. Command is the argv
// prefix of the fleetdeck binary; "runtime --key K --state-dir D" follows.
type StartSpec struct {
	StateDir   string
	Command    []string
	ConfigPath string
	CWD        string
	Board      string
	Card       string
	Wait       time.Duration
}

// Start allocates a runtime key of its own, separate from the native thread
// ID, records the intent and spawns the runtime. The key is returned even on
// failure: with ErrStarting the runtime may still come up and must not be
// started twice.
func Start(ctx context.Context, spec StartSpec) (executor.Key, Client, error) {
	key, err := newRuntimeKey()
	if err != nil {
		return "", Client{}, err
	}
	client, err := launch(ctx, spec, key, func(*Record) (Record, error) {
		return Record{
			Version: ProtocolVersion, Key: key, CWD: canonical(spec.CWD), Board: canonical(spec.Board), Card: canonical(spec.Card),
			ConfigPath: spec.ConfigPath, CreatedAt: time.Now().UTC(),
		}, nil
	})
	return key, client, err
}

// Resume brings back the runtime of a saved key, or joins it when it is
// already serving. Its native thread, if any, is resumed, never recreated.
func Resume(ctx context.Context, spec StartSpec, key executor.Key) (Client, error) {
	store, err := OpenStore(spec.StateDir)
	if err != nil {
		return Client{}, err
	}
	_, err = store.Read(string(key))
	_ = store.Close()
	if errors.Is(err, os.ErrNotExist) {
		return Client{}, executor.ErrNotResumable
	}
	if err != nil {
		return Client{}, err
	}
	return launch(ctx, spec, key, func(saved *Record) (Record, error) {
		if saved == nil {
			return Record{}, executor.ErrNotResumable
		}
		return *saved, nil
	})
}

// Connect addresses a serving runtime after its handshake. It never starts
// one: an unanswered socket alone is no reason for a replacement.
func Connect(ctx context.Context, stateDir string, key executor.Key) (Client, error) {
	store, err := OpenStore(stateDir)
	if err != nil {
		return Client{}, err
	}
	record, err := store.Read(string(key))
	_ = store.Close()
	if errors.Is(err, os.ErrNotExist) {
		return Client{}, executor.ErrNotResumable
	}
	if err != nil {
		return Client{}, err
	}
	socket, err := SocketPath(stateDir, key)
	if err != nil {
		return Client{}, err
	}
	c := Client{Socket: socket, Record: record}
	if err := c.Call(ctx, "hello", nil, nil); err != nil {
		return Client{}, err
	}
	return c, nil
}

// launch arbitrates by the key lock, not by PID or socket liveness: a held
// lock means a runtime owns the key, so it is joined or reported starting.
func launch(ctx context.Context, spec StartSpec, key executor.Key, prepare func(*Record) (Record, error)) (Client, error) {
	if len(spec.Command) == 0 {
		return Client{}, errors.New("runtime command is required")
	}
	if _, err := SocketPath(spec.StateDir, key); err != nil {
		return Client{}, err
	}
	store, err := OpenStore(spec.StateDir)
	if err != nil {
		return Client{}, err
	}
	defer func() { _ = store.Close() }()
	owner, err := acquireSettled(ctx, store, key)
	if errors.Is(err, executor.ErrConflict) {
		if record, rerr := store.Read(string(key)); rerr == nil && record.Lifecycle == "live" {
			if c, err := Connect(ctx, spec.StateDir, key); err == nil {
				return c, nil
			}
		}
		return Client{}, executor.ErrStarting
	}
	if err != nil {
		return Client{}, err
	}
	var saved *Record
	if existing, err := store.Read(string(key)); err == nil {
		saved = &existing
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = owner.Close()
		return Client{}, err
	}
	record, err := prepare(saved)
	if err != nil {
		_ = owner.Close()
		return Client{}, err
	}
	// A placeholder identity: no runtime answers to it, so clients wait for
	// the runtime to publish its own.
	record.PID, record.ProcessStartedAt, record.Nonce, record.Lifecycle = os.Getpid(), "", rand.Text()+rand.Text(), "starting"
	err = owner.Save(record)
	// The runtime takes the lock itself. A start racing into this gap spawns
	// a second runtime that loses the lock and exits.
	_ = owner.Close()
	if err != nil {
		return Client{}, err
	}
	argv := append(slices.Clone(spec.Command), "runtime", "--key", string(key), "--state-dir", spec.StateDir)
	cmd := exec.Command(argv[0], argv[1:]...)
	// Its own session: the runtime outlives the panel and its process group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return Client{}, fmt.Errorf("spawn Codex runtime: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	wait := spec.Wait
	if wait == 0 {
		wait = defaultStartWait
	}
	client, err := awaitRuntime(ctx, store, spec.StateDir, key, cmd.Process.Pid, exited, wait)
	if errors.Is(err, ErrRuntimeEnded) {
		settlePlaceholder(store, key, record.Nonce)
	}
	return client, err
}

// settlePlaceholder finalizes the intent record of a runtime that exited
// before publishing its own identity, so it does not claim "starting"
// forever. A record a runtime has taken over is left to that runtime.
func settlePlaceholder(store *Store, key executor.Key, nonce string) {
	owner, err := store.Acquire(key)
	if err != nil {
		return
	}
	defer func() { _ = owner.Close() }()
	record, err := store.Read(string(key))
	if err != nil || record.Nonce != nonce || record.Lifecycle != "starting" {
		return
	}
	record.Lifecycle = "dead"
	if record.ThreadID != "" {
		record.Lifecycle = "stopped"
	}
	_ = owner.Save(record)
}

func awaitRuntime(ctx context.Context, store *Store, stateDir string, key executor.Key, pid int, exited <-chan struct{}, wait time.Duration) (Client, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if record, err := store.Read(string(key)); err == nil && record.PID == pid {
			switch record.Lifecycle {
			case "live":
				if c, err := Connect(ctx, stateDir, key); err == nil && c.Record.PID == pid {
					return c, nil
				}
			case "stopped", "dead":
				return Client{}, ErrRuntimeEnded
			}
		}
		select {
		case <-exited:
			// It may have lost the key to a concurrent start that serves it.
			if c, err := Connect(ctx, stateDir, key); err == nil {
				return c, nil
			}
			return Client{}, ErrRuntimeEnded
		case <-timer.C:
			return Client{}, executor.ErrStarting
		case <-ctx.Done():
			return Client{}, fmt.Errorf("%w: %w", executor.ErrStarting, ctx.Err())
		case <-ticker.C:
		}
	}
}

func newRuntimeKey() (executor.Key, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return executor.Key(fmt.Sprintf("codex:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])), nil
}

// keyRelease bounds the wait for a runtime that has written its final record
// and is still exiting with the key lock held.
const keyRelease = 5 * time.Second

// acquireSettled takes the key lock, waiting briefly while the record already
// says the runtime ended: that holder is exiting, not serving.
func acquireSettled(ctx context.Context, store *Store, key executor.Key) (*Owner, error) {
	deadline := time.Now().Add(keyRelease)
	for {
		owner, err := store.Acquire(key)
		if !errors.Is(err, executor.ErrConflict) {
			return owner, err
		}
		record, rerr := store.Read(string(key))
		if rerr != nil || !slices.Contains([]string{"stopping", "stopped", "dead", "unknown"}, record.Lifecycle) || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// canonical resolves symlinks, so a card and its board are compared, and
// recorded, by the same path however each was named (/tmp is a link on
// macOS). A path not there yet is resolved through its nearest existing
// parent.
func canonical(path string) string {
	rest := ""
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		if parent := filepath.Dir(dir); parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}
