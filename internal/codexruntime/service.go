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
	// Activity is working, idle, error or unknown, as last observed at
	// ObservedAt; a lost connection makes it and NeedsHuman unknown.
	Activity   string           `json:"activity"`
	NeedsHuman executor.Verdict `json:"needsHuman"`
	ObservedAt time.Time        `json:"observedAt"`
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
	state := &runtimeState{owner: owner, record: record}
	// Every exit leaves a final lifecycle: stopped when the native thread can
	// be resumed, dead when none was registered.
	final := ""
	defer func() {
		saveErr := state.update(func(r *Record) error {
			r.Lifecycle = final
			if r.ThreadID == "" {
				r.Lifecycle = "dead"
			} else if final == "" {
				r.Lifecycle = "stopped"
			}
			return nil
		})
		if err == nil {
			err = saveErr
		}
	}()

	if spec.EnsureNative != nil {
		if err := spec.EnsureNative(ctx); err != nil {
			return fmt.Errorf("start Codex app-server: %w", err)
		}
	}
	obs := &observer{}
	native, err := openNativeControl(ctx, spec.NativeSocket, obs.handle)
	if err != nil {
		return fmt.Errorf("connect to Codex app-server: %w", err)
	}
	link := &nativeLink{current: native, socket: spec.NativeSocket, obs: obs}
	defer link.Close()
	if err := registerThread(ctx, native, state, spec.Thread); err != nil {
		return err
	}
	record = state.snapshot()
	link.thread = record.ThreadID
	if err := link.reconcile(ctx, native); err != nil {
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
	if err := state.update(func(r *Record) error { r.Lifecycle = "live"; return nil }); err != nil {
		return err
	}

	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	handle := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "status":
			current := state.snapshot()
			native, nerr := link.get()
			pending := 0
			if nerr == nil {
				pending = len(native.pending())
			} else {
				obs.lost()
			}
			activity, needs, at := obs.view(pending)
			return Status{Lifecycle: current.Lifecycle, ThreadID: current.ThreadID, Native: nerr == nil, Terminal: !isDone(term.done), Activity: activity, NeedsHuman: needs, ObservedAt: at}, nil
		case "terminal.attach":
			var req struct {
				Offset *uint64 `json:"offset"`
			}
			if len(params) > 0 && json.Unmarshal(params, &req) != nil {
				return nil, ErrInvalidFrame
			}
			// A typed nil in `any` would pass as a streamer.
			attached, err := term.attachStream(ctx, req.Offset)
			if err != nil {
				return nil, err
			}
			return attached, nil
		}
		// Everything below needs the native connection.
		native, err := link.get()
		if err != nil {
			return nil, err
		}
		switch method {
		case "requests":
			return pendingRequests(native), nil
		case "requests.respond":
			var a Answer
			if json.Unmarshal(params, &a) != nil {
				return nil, ErrInvalidFrame
			}
			return struct{}{}, respondRequest(ctx, native, a)
		case "send":
			var req struct {
				OperationID string `json:"operationId"`
				Text        string `json:"text"`
			}
			if json.Unmarshal(params, &req) != nil {
				return nil, ErrInvalidFrame
			}
			return deliver(ctx, native, state, req.OperationID, req.Text)
		case "history":
			var req struct {
				Cursor string `json:"cursor"`
				Limit  int    `json:"limit"`
			}
			if len(params) > 0 && json.Unmarshal(params, &req) != nil {
				return nil, ErrInvalidFrame
			}
			return readHistory(ctx, native, record.ThreadID, req.Cursor, req.Limit)
		case "lastWords":
			return lastWords(ctx, native, record.ThreadID)
		case "interrupt":
			active, err := activeTurns(ctx, native, record.ThreadID)
			for _, turn := range active {
				if err == nil {
					err = native.call(ctx, "turn/interrupt", map[string]string{"threadId": record.ThreadID, "turnId": turn}, nil)
				}
			}
			return struct{}{}, err
		case "stop":
			// Work is proven stopped before the TUI closes; on failure the
			// session keeps running and the caller sees why.
			if err := stopWork(ctx, native, record.ThreadID); err != nil {
				return nil, err
			}
			shutdownOnce.Do(func() { close(shutdown) })
			return struct{}{}, nil
		case "shutdown":
			shutdownOnce.Do(func() { close(shutdown) })
			return struct{}{}, nil
		}
		return nil, executor.ErrUnsupported
	}
	identity := state.snapshot()
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	go link.maintain(serveCtx)
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
	final = "unknown"
	if native, err := link.get(); err == nil {
		final = settledLifecycle(native, record.ThreadID)
	}
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
func registerThread(ctx context.Context, native *nativeControl, state *runtimeState, extra map[string]any) error {
	record := state.snapshot()
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
	if err := state.update(func(r *Record) error { r.ThreadID = record.ThreadID; return nil }); err != nil {
		return err
	}
	native.own(record.ThreadID)
	// A name persists the still-empty rollout so ordinary resume can open it
	// without a model turn.
	name := strings.TrimSuffix(filepath.Base(record.Card), ".md")
	return native.call(ctx, "thread/name/set", map[string]string{"threadId": record.ThreadID, "name": name}, nil)
}
