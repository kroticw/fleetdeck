package codexruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/executor"
)

// fakeNative answers the bootstrap methods and records their order. push
// sends a notification on the current connection; drop closes it, and the
// next connection is served afresh.
type fakeNative struct {
	mu      sync.Mutex
	methods []string
	socket  string
	status  string
	conns   int
	push    chan any
	drop    chan struct{}
}

func newFakeNative(t *testing.T) *fakeNative {
	t.Helper()
	f := &fakeNative{status: "idle", push: make(chan any, 8), drop: make(chan struct{}, 1)}
	f.socket = nativeServer(t, func(ctx context.Context, conn *websocket.Conn) {
		f.mu.Lock()
		f.conns++
		f.mu.Unlock()
		incoming := make(chan nativeMessage)
		go func() {
			defer close(incoming)
			for {
				// The runtime closing its connection ends the fake, not the test.
				_, raw, err := conn.Read(ctx)
				if err != nil {
					return
				}
				var m nativeMessage
				if json.Unmarshal(raw, &m) != nil || m.Method == "" {
					t.Errorf("unexpected native frame %s", raw)
					return
				}
				incoming <- m
			}
		}()
		write := func(v any) bool {
			raw, _ := json.Marshal(v)
			return conn.Write(ctx, websocket.MessageText, raw) == nil
		}
		for {
			var m nativeMessage
			var ok bool
			select {
			case m, ok = <-incoming:
				if !ok {
					return
				}
			case v := <-f.push:
				if !write(v) {
					return
				}
				continue
			case <-f.drop:
				_ = conn.CloseNow()
				return
			}
			f.mu.Lock()
			f.methods = append(f.methods, m.Method)
			status := f.status
			f.mu.Unlock()
			result := map[string]any{}
			if m.Method == "thread/turns/list" || m.Method == "thread/backgroundTerminals/list" {
				result["data"] = []any{}
			}
			if m.Method == "thread/start" || m.Method == "thread/resume" || m.Method == "thread/read" {
				result["thread"] = map[string]any{"id": nativeThread, "status": map[string]string{"type": status}}
			}
			if !write(map[string]any{"id": m.ID, "result": result}) {
				return
			}
		}
	})
	return f
}

func (f *fakeNative) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

type serviceFixture struct {
	dir      string
	spec     ServiceSpec
	launched chan string
	native   *fakeNative
}

func newServiceFixture(t *testing.T, threadID string) *serviceFixture {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fleetdeck-svc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	state := filepath.Join(dir, "state")
	store, err := OpenStore(state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	owner, err := store.Acquire(testRuntimeKey)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeRecord()
	record.CWD, record.Board, record.Card = dir, dir, filepath.Join(dir, "cards", "T-001-task.md")
	record.ThreadID = threadID
	if err := owner.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEETDECK_RUNTIME_PTY_TEST", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &serviceFixture{dir: state, launched: make(chan string, 1), native: newFakeNative(t)}
	f.spec = ServiceSpec{
		StateDir: state, Key: testRuntimeKey, NativeSocket: f.native.socket, Cols: 80, Rows: 24,
		Launch: func(thread string) ([]string, []string, error) {
			f.launched <- thread
			return []string{binary, "-test.run=^TestPTYChild$"}, os.Environ(), nil
		},
	}
	return f
}

func (f *serviceFixture) run(t *testing.T) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	finished := make(chan struct{})
	go func() { served <- Serve(ctx, f.spec); close(finished) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("service did not stop with its context")
		}
	})
	return served
}

func (f *serviceFixture) awaitLive(t *testing.T, served <-chan error) Client {
	t.Helper()
	store, err := OpenStore(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	deadline := time.After(5 * time.Second)
	for {
		record, err := store.Read(string(testRuntimeKey))
		if err == nil && record.Lifecycle == "live" {
			socket, err := SocketPath(f.dir, testRuntimeKey)
			if err != nil {
				t.Fatal(err)
			}
			return Client{Socket: socket, Record: record}
		}
		select {
		case err := <-served:
			t.Fatalf("service ended before live: %v", err)
		case <-deadline:
			t.Fatalf("service never became live: %+v %v", record, err)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (f *serviceFixture) record(t *testing.T) Record {
	t.Helper()
	store, err := OpenStore(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	record, err := store.Read(string(testRuntimeKey))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func awaitServed(t *testing.T, served <-chan error) error {
	t.Helper()
	select {
	case err := <-served:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("service did not end")
		return nil
	}
}

func TestServiceRegistersThreadBeforeTerminalAndServesViews(t *testing.T) {
	f := newServiceFixture(t, "")
	served := f.run(t)
	client := f.awaitLive(t, served)
	if got := <-f.launched; got != nativeThread {
		t.Fatalf("TUI resumed %q", got)
	}
	record := f.record(t)
	if record.ThreadID != nativeThread || record.PID != os.Getpid() || record.Nonce == runtimeRecord().Nonce {
		t.Fatalf("record does not identify this runtime and thread: %+v", record)
	}
	if seen := f.native.seen(); len(seen) < 2 || seen[0] != "thread/start" || seen[1] != "thread/name/set" {
		t.Fatalf("native bootstrap order: %v", seen)
	}
	var pending []PendingRequest
	if err := client.Call(context.Background(), "requests", nil, &pending); err != nil || len(pending) != 0 {
		t.Fatalf("requests %v %v", pending, err)
	}
	if err := client.Call(context.Background(), "requests.respond", Answer{Token: "stale", Decision: "accept"}, nil); !errors.Is(err, executor.ErrConflict) {
		t.Fatalf("stale answer: %v", err)
	}
	var receipt executor.Receipt
	send := map[string]string{"operationId": testOperation, "text": "do the task"}
	if err := client.Call(context.Background(), "send", send, &receipt); err != nil || receipt.State != executor.Accepted {
		t.Fatalf("send %+v %v", receipt, err)
	}
	if op := f.record(t).Operations[testOperation]; op.Phase != string(executor.Accepted) {
		t.Fatalf("send was not journaled: %+v", op)
	}
	var history History
	if err := client.Call(context.Background(), "history", map[string]int{"limit": 5}, &history); err != nil || len(history.Entries) != 0 {
		t.Fatalf("history %+v %v", history, err)
	}
	var status Status
	if err := client.Call(context.Background(), "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	if status.Lifecycle != "live" || status.ThreadID != nativeThread || !status.Native || !status.Terminal {
		t.Fatalf("status: %+v", status)
	}
	s := attachStream(t, client, nil)
	if err := s.Input([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	awaitStream(t, s, "SIZE=80x24")
	if err := client.Call(context.Background(), "stop", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := awaitServed(t, served); err != nil {
		t.Fatal(err)
	}
	if record := f.record(t); record.Lifecycle != "stopped" || record.ThreadID != nativeThread {
		t.Fatalf("stop record: %+v", record)
	}
	for _, method := range []string{"thread/turns/list", "thread/backgroundTerminals/clean", "thread/backgroundTerminals/list"} {
		if !slices.Contains(f.native.seen(), method) {
			t.Fatalf("stop skipped %s: %v", method, f.native.seen())
		}
	}
}

func TestServiceResumesSavedThreadWithoutStartingAnother(t *testing.T) {
	f := newServiceFixture(t, nativeThread)
	served := f.run(t)
	f.awaitLive(t, served)
	for _, method := range f.native.seen() {
		if method == "thread/start" {
			t.Fatal("resume created a second native thread")
		}
	}
	if seen := f.native.seen(); len(seen) == 0 || seen[0] != "thread/resume" {
		t.Fatalf("native resume: %v", seen)
	}
}

func TestServiceRefusesAKeyAnotherProcessOwns(t *testing.T) {
	f := newServiceFixture(t, "")
	store, err := OpenStore(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	owner, err := store.Acquire(testRuntimeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	if err := awaitServed(t, f.run(t)); !errors.Is(err, executor.ErrConflict) {
		t.Fatalf("second owner: %v", err)
	}
	if len(f.native.seen()) != 0 {
		t.Fatal("a refused runtime touched the native thread")
	}
}

func TestServiceRecordsTerminalExitAsStopped(t *testing.T) {
	f := newServiceFixture(t, "")
	served := f.run(t)
	client := f.awaitLive(t, served)
	s := attachStream(t, client, nil)
	if err := s.Input([]byte("finish\n")); err != nil {
		t.Fatal(err)
	}
	if err := awaitServed(t, served); err != nil {
		t.Fatal(err)
	}
	if record := f.record(t); record.Lifecycle != "stopped" {
		t.Fatalf("exit record: %+v", record)
	}
}

func TestServiceWithoutNativeControlRecordsNoThread(t *testing.T) {
	f := newServiceFixture(t, "")
	f.spec.NativeSocket = filepath.Join(f.dir, "missing.sock")
	if err := awaitServed(t, f.run(t)); err == nil {
		t.Fatal("service started without native control")
	}
	if record := f.record(t); record.Lifecycle != "dead" || record.ThreadID != "" {
		t.Fatalf("failed bootstrap record: %+v", record)
	}
	select {
	case <-f.launched:
		t.Fatal("TUI launched without a registered thread")
	default:
	}
}

func TestServiceDoesNotCallBusyNativeWorkStopped(t *testing.T) {
	f := newServiceFixture(t, "")
	f.native.mu.Lock()
	f.native.status = "active"
	f.native.mu.Unlock()
	served := f.run(t)
	client := f.awaitLive(t, served)
	s := attachStream(t, client, nil)
	// The TUI exits, but Codex keeps working natively.
	if err := s.Input([]byte("finish\n")); err != nil {
		t.Fatal(err)
	}
	if err := awaitServed(t, served); err != nil {
		t.Fatal(err)
	}
	if record := f.record(t); record.Lifecycle != "unknown" {
		t.Fatalf("busy native thread recorded as %s", record.Lifecycle)
	}
}

func TestServiceSurvivesARejectedAttach(t *testing.T) {
	f := newServiceFixture(t, "")
	served := f.run(t)
	client := f.awaitLive(t, served)
	beyond := uint64(1) << 40
	if _, err := client.AttachTerminal(context.Background(), &beyond); err == nil {
		t.Fatal("attach beyond the output succeeded")
	}
	var status Status
	if err := client.Call(context.Background(), "status", nil, &status); err != nil || status.Lifecycle != "live" {
		t.Fatalf("runtime did not survive a rejected attach: %+v %v", status, err)
	}
}

func awaitStatus(t *testing.T, client Client, ok func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status Status
		err := client.Call(context.Background(), "status", nil, &status)
		if err == nil && ok(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never matched: %+v %v", status, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServiceObservesThreadAndReconnectsToNative(t *testing.T) {
	f := newServiceFixture(t, "")
	served := f.run(t)
	client := f.awaitLive(t, served)
	awaitStatus(t, client, func(s Status) bool {
		return s.Activity == "idle" && s.NeedsHuman == executor.No && !s.ObservedAt.IsZero()
	})
	f.native.push <- map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": nativeThread, "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}}}
	awaitStatus(t, client, func(s Status) bool { return s.Activity == "working" && s.NeedsHuman == executor.Yes })
	// Another thread's (e.g. a subagent's) idle event does not touch ours.
	f.native.push <- map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": foreignThread, "status": map[string]any{"type": "idle"}}}
	f.native.drop <- struct{}{}
	lost := awaitStatus(t, client, func(s Status) bool { return !s.Native })
	if lost.NeedsHuman != executor.Unknown || lost.Activity != "unknown" {
		t.Fatalf("a lost observation must be unknown: %+v", lost)
	}
	// Native comes back before the reconcile has read the thread's status;
	// until then the state is honestly unknown, so the wait is for both.
	awaitStatus(t, client, func(s Status) bool { return s.Native && s.Activity == "idle" && s.NeedsHuman == executor.No })
	f.native.mu.Lock()
	defer f.native.mu.Unlock()
	if f.native.conns < 2 || !slices.Contains(f.native.methods[1:], "thread/resume") {
		t.Fatalf("reconnect did not resume the thread: conns=%d %v", f.native.conns, f.native.methods)
	}
}
