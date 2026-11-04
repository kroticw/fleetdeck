package codexruntime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

const defaultTerminalBuffer = 1 << 20

// maxSocketPath leaves room for the terminating NUL in macOS sun_path.
const maxSocketPath = 103

// ServiceSpec is what one runtime process needs besides its saved record.
type ServiceSpec struct {
	StateDir string
	Key      executor.Key
	// NativeSocket is CODEX_HOME's app-server control entry.
	NativeSocket string
	// EnsureNative starts Codex's own local daemon when it is not running;
	// nil assumes it is.
	EnsureNative func(context.Context) error
	// Thread holds thread/start parameters besides cwd, e.g. model.
	Thread map[string]any
	// Launch builds the ordinary `codex resume <thread>` command.
	Launch      func(threadID string) (argv, env []string, err error)
	Cols, Rows  int
	BufferLimit int
}

// Status is the runtime's own view; it reports facts it holds, nothing parsed
// from the TUI.
type Status struct {
	Lifecycle string `json:"lifecycle"`
	ThreadID  string `json:"threadId"`
	Native    bool   `json:"native"`
	Terminal  bool   `json:"terminal"`
}

// SocketPath is the runtime's private IPC endpoint inside stateDir.
func SocketPath(stateDir string, raw executor.Key) (string, error) {
	_, name, err := recordName(string(raw))
	if err != nil {
		return "", err
	}
	path := filepath.Join(stateDir, strings.ReplaceAll(name, "-", "")+".sock")
	if len(path) > maxSocketPath {
		return "", fmt.Errorf("runtime socket path %s exceeds the Unix socket limit", path)
	}
	return path, nil
}

// Serve owns one Codex session until its context ends, a client asks it to
// shut down, or the TUI exits. It refuses a key another process holds, and
// saves the native thread before any TUI or card can use it.
func Serve(ctx context.Context, spec ServiceSpec) (err error) {
	if spec.Launch == nil || !validGeometry(spec.Cols, spec.Rows) {
		return errors.New("runtime service needs a launcher and a valid geometry")
	}
	limit := spec.BufferLimit
	if limit == 0 {
		limit = defaultTerminalBuffer
	}
	store, err := OpenStore(spec.StateDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	owner, err := store.Acquire(spec.Key)
	if err != nil {
		return err
	}
	defer func() { _ = owner.Close() }()
	record, err := store.Read(string(spec.Key))
	if err != nil {
		return err
	}
	socket, err := SocketPath(spec.StateDir, spec.Key)
	if err != nil {
		return err
	}
	record.PID = os.Getpid()
	record.ProcessStartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	record.Nonce = rand.Text() + rand.Text()
	record.Lifecycle = "starting"

	// The key lock is held, so a socket left at this path is a previous
	// owner's. Remove drops a symlink itself, never its target.
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close(); _ = os.Remove(socket) }()
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	if err := owner.Save(record); err != nil {
		return err
	}
	// Every exit leaves a final lifecycle: stopped when the native thread can
	// be resumed, dead when none was registered.
	final := ""
	defer func() {
		record.Lifecycle = final
		if record.ThreadID == "" {
			record.Lifecycle = "dead"
		} else if final == "" {
			record.Lifecycle = "stopped"
		}
		if saveErr := owner.Save(record); err == nil {
			err = saveErr
		}
	}()

	if spec.EnsureNative != nil {
		if err := spec.EnsureNative(ctx); err != nil {
			return fmt.Errorf("start Codex app-server: %w", err)
		}
	}
	native, err := openNativeControl(ctx, spec.NativeSocket, nil)
	if err != nil {
		return fmt.Errorf("connect to Codex app-server: %w", err)
	}
	defer native.Close()
	if err := registerThread(ctx, native, owner, &record, spec.Thread); err != nil {
		return err
	}

	argv, env, err := spec.Launch(record.ThreadID)
	if err != nil {
		return err
	}
	term, err := startTerminal(ctx, argv, env, record.CWD, spec.Cols, spec.Rows, limit)
	if err != nil {
		return err
	}
	defer term.Close()
	record.Lifecycle = "live"
	if err := owner.Save(record); err != nil {
		return err
	}

	var mu sync.Mutex
	lifecycle := record.Lifecycle
	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	handle := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "status":
			mu.Lock()
			defer mu.Unlock()
			return Status{Lifecycle: lifecycle, ThreadID: record.ThreadID, Native: !isDone(native.done), Terminal: !isDone(term.done)}, nil
		case "terminal.attach":
			var req struct {
				Offset *uint64 `json:"offset"`
			}
			if len(params) > 0 && json.Unmarshal(params, &req) != nil {
				return nil, ErrInvalidFrame
			}
			return term.attachStream(ctx, req.Offset)
		case "shutdown":
			shutdownOnce.Do(func() { close(shutdown) })
			return struct{}{}, nil
		}
		return nil, executor.ErrUnsupported
	}
	identity := record
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { _ = serveConn(serveCtx, conn, identity, handle) }()
		}
	}()

	select {
	case <-ctx.Done():
	case <-shutdown:
	case <-term.done:
	}
	term.Close()
	final = settledLifecycle(native, record.ThreadID)
	mu.Lock()
	lifecycle = final
	mu.Unlock()
	return nil
}

// settledLifecycle is what ending the TUI leaves behind. Closing the TUI
// does not stop native work, so only an idle thread counts as stopped; a
// busy or unobservable one is unknown until stop/resume reconciles it.
func settledLifecycle(native *nativeControl, thread string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var state struct {
		Thread struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if native.call(ctx, "thread/read", map[string]any{"threadId": thread, "includeTurns": false}, &state) != nil {
		return "unknown"
	}
	switch state.Thread.Status.Type {
	case "idle", "notLoaded":
		return "stopped"
	}
	return "unknown"
}

// registerThread creates and names a new thread, saving its ID before the
// name or any TUI can use it, or rejoins the saved one. Ownership is taken
// before resume so that its replayed requests are kept.
func registerThread(ctx context.Context, native *nativeControl, owner *Owner, record *Record, extra map[string]any) error {
	if record.ThreadID != "" {
		native.own(record.ThreadID)
		return native.call(ctx, "thread/resume", map[string]any{"threadId": record.ThreadID, "excludeTurns": true}, nil)
	}
	params := maps.Clone(extra)
	if params == nil {
		params = map[string]any{}
	}
	params["cwd"] = record.CWD
	params["serviceName"] = "fleetdeck"
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := native.call(ctx, "thread/start", params, &started); err != nil {
		return fmt.Errorf("start Codex thread: %w", err)
	}
	if _, _, err := executor.ParseKey("codex:" + started.Thread.ID); err != nil {
		return fmt.Errorf("codex returned an invalid thread ID: %w", err)
	}
	record.ThreadID = started.Thread.ID
	if err := owner.Save(*record); err != nil {
		return err
	}
	native.own(record.ThreadID)
	// A name persists the still-empty rollout so ordinary resume can open it
	// without a model turn.
	name := strings.TrimSuffix(filepath.Base(record.Card), ".md")
	return native.call(ctx, "thread/name/set", map[string]string{"threadId": record.ThreadID, "name": name}, nil)
}
