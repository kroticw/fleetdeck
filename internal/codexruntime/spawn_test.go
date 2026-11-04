package codexruntime

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

// TestRuntimeChild is the runtime process for spawn tests: `-- runtime --key
// K --state-dir D` after the test flags, serving the PTY test child.
func TestRuntimeChild(_ *testing.T) {
	socket := os.Getenv("FLEETDECK_RUNTIME_SERVICE_NATIVE")
	if socket == "" {
		return
	}
	args := flag.Args()
	if len(args) != 5 || args[0] != "runtime" || args[1] != "--key" || args[3] != "--state-dir" {
		os.Exit(2)
	}
	binary, _ := os.Executable()
	err := Serve(context.Background(), ServiceSpec{
		StateDir: args[4], Key: executor.Key(args[2]), NativeSocket: socket, Cols: 80, Rows: 24,
		Launch: func(string) ([]string, []string, error) {
			return []string{binary, "-test.run=^TestPTYChild$"}, append(os.Environ(), "FLEETDECK_RUNTIME_PTY_TEST=1"), nil
		},
	})
	if err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

// TestPanelChild plays a panel that starts a runtime, prints its key and
// exits, as a panel restart would.
func TestPanelChild(_ *testing.T) {
	if os.Getenv("FLEETDECK_RUNTIME_PANEL_TEST") != "1" {
		return
	}
	spec := spawnSpec(os.Getenv("FLEETDECK_RUNTIME_STATE"), os.Getenv("FLEETDECK_RUNTIME_DIR"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key, _, err := Start(ctx, spec)
	if err != nil {
		os.Exit(4)
	}
	_, _ = fmt.Fprintln(os.Stdout, "KEY="+string(key))
	os.Exit(0)
}

func spawnSpec(state, dir string) StartSpec {
	binary, _ := os.Executable()
	return StartSpec{
		StateDir: state, Command: []string{binary, "-test.run=^TestRuntimeChild$", "--"},
		CWD: dir, Board: dir, Card: filepath.Join(dir, "cards", "T-002-spawn.md"),
	}
}

func spawnFixture(t *testing.T) StartSpec {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fleetdeck-spawn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	native := newFakeNative(t)
	t.Setenv("FLEETDECK_RUNTIME_SERVICE_NATIVE", native.socket)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	return spawnSpec(filepath.Join(dir, "state"), dir)
}

func stopRuntime(t *testing.T, state string, key executor.Key) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if c, err := Connect(ctx, state, key); err == nil {
			_ = c.Call(ctx, "shutdown", nil, nil)
		}
	})
}

func TestStartRegistersThreadThroughASeparateRuntime(t *testing.T) {
	spec := spawnFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key, client, err := Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	stopRuntime(t, spec.StateDir, key)
	if id, _, err := executor.ParseKey(string(key)); err != nil || id != executor.Codex || key == executor.Key("codex:"+nativeThread) {
		t.Fatalf("runtime key %q must be its own UUID, not the native thread", key)
	}
	if client.Record.PID == os.Getpid() || client.Record.ThreadID != nativeThread || client.Record.Lifecycle != "live" {
		t.Fatalf("client does not address the spawned runtime: %+v", client.Record)
	}
	var status Status
	if err := client.Call(ctx, "status", nil, &status); err != nil || status.ThreadID != nativeThread {
		t.Fatalf("status %+v %v", status, err)
	}
	// A second start of the same key while it runs joins it, never a twin.
	again, err := Resume(ctx, spec, key)
	if err != nil || again.Record.PID != client.Record.PID {
		t.Fatalf("resume of a live key: %+v %v", again.Record, err)
	}
}

func TestRuntimeOutlivesThePanelThatStartedIt(t *testing.T) {
	spec := spawnFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	panel := exec.Command(binary, "-test.run=^TestPanelChild$")
	panel.Env = append(os.Environ(), "FLEETDECK_RUNTIME_PANEL_TEST=1", "FLEETDECK_RUNTIME_STATE="+spec.StateDir, "FLEETDECK_RUNTIME_DIR="+spec.CWD)
	out, err := panel.Output()
	if err != nil {
		t.Fatalf("panel: %v", err)
	}
	var key executor.Key
	for line := range strings.Lines(string(out)) {
		if k, ok := strings.CutPrefix(strings.TrimSpace(line), "KEY="); ok {
			key = executor.Key(k)
		}
	}
	if key == "" {
		t.Fatalf("panel printed no key: %q", out)
	}
	stopRuntime(t, spec.StateDir, key)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Connect(ctx, spec.StateDir, key)
	if err != nil {
		t.Fatalf("runtime died with its panel: %v", err)
	}
	s := attachStream(t, client, nil)
	if err := s.Input([]byte("size\n")); err != nil {
		t.Fatal(err)
	}
	awaitStream(t, s, "SIZE=80x24")
}

func TestStartReportsARuntimeThatFailsToBecomeLive(t *testing.T) {
	spec := spawnFixture(t)
	t.Setenv("FLEETDECK_RUNTIME_SERVICE_NATIVE", filepath.Join(spec.CWD, "missing.sock"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key, _, err := Start(ctx, spec)
	if err == nil || errors.Is(err, executor.ErrStarting) {
		t.Fatalf("failed runtime: %v", err)
	}
	if key == "" {
		t.Fatal("a failed start must still name its key for diagnosis")
	}
}

func TestResumeRefusesAnUnknownKey(t *testing.T) {
	spec := spawnFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := Resume(ctx, spec, testRuntimeKey); !errors.Is(err, executor.ErrNotResumable) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestStartSettlesTheRecordOfARuntimeThatNeverServed(t *testing.T) {
	spec := spawnFixture(t)
	spec.Command = []string{"/usr/bin/false"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, _, err := Start(ctx, spec)
	if !errors.Is(err, ErrRuntimeEnded) {
		t.Fatalf("runtime that never ran: %v", err)
	}
	store, err := OpenStore(spec.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if record, err := store.Read(string(key)); err != nil || record.Lifecycle != "dead" {
		t.Fatalf("placeholder left behind: %+v %v", record, err)
	}
}
