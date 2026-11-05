package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/executor"
)

const liveProbeMarker = "disposable Fleetdeck integration probe\n"

// TestRuntimeLiveCleanStart runs the built fleetdeck binary against real
// Codex in a disposable, marked CODEX_HOME. It registers a thread and opens
// the TUI without any model turn. Opt-in only.
func TestRuntimeLiveCleanStart(t *testing.T) {
	home, binary := os.Getenv("FLEETDECK_LIVE_CODEX_HOME"), os.Getenv("FLEETDECK_LIVE_BINARY")
	if home == "" || binary == "" {
		t.Skip("explicit disposable CODEX_HOME and built binary required")
	}
	user, _ := os.UserHomeDir()
	if !filepath.IsAbs(home) || filepath.Clean(home) == filepath.Join(user, ".codex") || filepath.Clean(home) == filepath.Clean(os.Getenv("CODEX_HOME")) {
		t.Fatal("live runtime refuses the user's real CODEX_HOME")
	}
	if marker, err := os.ReadFile(filepath.Join(home, ".fleetdeck-probe")); err != nil || string(marker) != liveProbeMarker {
		t.Fatal("CODEX_HOME is not a marked disposable probe home")
	}
	dir, err := os.MkdirTemp("/tmp", "fdlive-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("executors:\n  codex:\n    enabled: true\n    home: "+home+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, "codex", "app-server", "daemon", "stop")
		stop.Env = append(os.Environ(), "CODEX_HOME="+home)
		_ = stop.Run()
	})
	spec := codexruntime.StartSpec{
		StateDir: filepath.Join(dir, "state"), Command: []string{binary}, ConfigPath: configPath,
		CWD: dir, Board: dir, Card: filepath.Join(dir, "cards", "T-900-live.md"), Wait: 60 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	key, client, err := codexruntime.Start(ctx, spec)
	if err != nil {
		t.Fatalf("start %s: %v", key, err)
	}
	var status codexruntime.Status
	if err := client.Call(ctx, "status", nil, &status); err != nil || status.ThreadID == "" || !status.Native || !status.Terminal {
		t.Fatalf("status %+v %v", status, err)
	}
	t.Logf("runtime %s serves native thread %s", key, status.ThreadID)
	stream, err := client.AttachTerminal(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var screen bytes.Buffer
	for !bytes.Contains(screen.Bytes(), []byte("\x1b[?2026l")) {
		ev, err := stream.Next(time.Now().Add(20 * time.Second))
		if err != nil || ev.End != "" {
			t.Fatalf("TUI produced no complete frame: %v %q (%d bytes)", err, ev.End, screen.Len())
		}
		screen.Write(ev.Data)
	}
	_ = stream.Close()
	// The API paths the panel relies on, on a thread with no turns yet.
	var history codexruntime.History
	if err := client.Call(ctx, "history", map[string]int{"limit": 5}, &history); err != nil || len(history.Entries) != 0 {
		t.Fatalf("history %+v %v", history, err)
	}
	var pending []codexruntime.PendingRequest
	if err := client.Call(ctx, "requests", nil, &pending); err != nil || len(pending) != 0 {
		t.Fatalf("requests %+v %v", pending, err)
	}
	var said executor.Said
	if err := client.Call(ctx, "lastWords", nil, &said); err != nil || !said.NoFinalAnswer {
		t.Fatalf("last words %+v %v", said, err)
	}
	if status.Activity != "idle" || status.NeedsHuman != executor.No {
		t.Fatalf("an unused thread must read idle with no one needed: %+v", status)
	}
	if err := client.Call(ctx, "stop", nil, nil); err != nil {
		t.Fatalf("stop: %v", err)
	}
	store, err := codexruntime.OpenStore(spec.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		record, err := store.Read(string(key))
		if err == nil && record.Lifecycle == "stopped" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record after stop: %+v %v", record, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Resume rejoins the same native thread under the same key.
	resumed, err := codexruntime.Resume(ctx, spec, key)
	if err != nil || resumed.Record.ThreadID != status.ThreadID {
		t.Fatalf("resume: %+v %v", resumed.Record, err)
	}
	if err := resumed.Call(ctx, "stop", nil, nil); err != nil {
		t.Fatalf("stop after resume: %v", err)
	}
}
