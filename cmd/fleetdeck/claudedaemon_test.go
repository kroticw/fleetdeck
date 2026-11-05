package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
)

// fakeDaemonClaude is a claude whose `daemon run` marks itself up, and down
// again when it is told to stop.
func fakeDaemonClaude(t *testing.T) (bin, up, down string) {
	t.Helper()
	dir := t.TempDir()
	up, down = filepath.Join(dir, "up"), filepath.Join(dir, "down")
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\n[ \"$1 $2\" = \"daemon run\" ] || exit 9\ntrap 'touch " + down + "; exit 0' TERM\ntouch " + up + "\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, up, down
}

func TestThePanelStartsAMissingDaemonAndPutsOutOnlyItsOwn(t *testing.T) {
	bin, up, down := fakeDaemonClaude(t)
	list := func(context.Context) ([]daemon.Session, error) {
		if exists(up) && !exists(down) {
			return nil, nil
		}
		return nil, daemon.ErrDaemonUnavailable
	}
	owned, err := ensureDaemon(context.Background(), runOpts{}, config.AgentConfig{Command: []string{bin}}, list)
	if err != nil || owned == nil {
		t.Fatalf("owned=%v err=%v", owned, err)
	}
	owned.Stop()
	if !exists(down) {
		t.Fatal("closing the panel left its daemon running")
	}
}

func TestThePanelNeverStartsADaemonThatIsRunningOrOnAStand(t *testing.T) {
	bin, up, _ := fakeDaemonClaude(t)
	running := func(context.Context) ([]daemon.Session, error) { return nil, nil }
	if owned, err := ensureDaemon(context.Background(), runOpts{}, config.AgentConfig{Command: []string{bin}}, running); owned != nil || err != nil {
		t.Fatalf("a running daemon was taken over: %v %v", owned, err)
	}
	missing := func(context.Context) ([]daemon.Session, error) { return nil, daemon.ErrDaemonUnavailable }
	if owned, err := ensureDaemon(context.Background(), runOpts{standSocket: "/tmp/no.sock"}, config.AgentConfig{Command: []string{bin}}, missing); owned != nil || err != nil {
		t.Fatalf("a stand started a daemon: %v %v", owned, err)
	}
	if exists(up) {
		t.Fatal("claude daemon run was started")
	}
	var nilDaemon *ownedDaemon
	nilDaemon.Stop()
}

func TestADaemonThatDiesAtStartIsReported(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	missing := func(context.Context) ([]daemon.Session, error) { return nil, daemon.ErrDaemonUnavailable }
	if _, err := ensureDaemon(context.Background(), runOpts{}, config.AgentConfig{Command: []string{bin}}, missing); err == nil {
		t.Fatal("a daemon that exited was reported started")
	}
}

func TestOpeningAFleetWakesItsPinnedOrchestratorOnce(t *testing.T) {
	var mu sync.Mutex
	var resumed []string
	alive := map[string]bool{"live0001": true}
	w := &orchestratorWaker{
		fleets: func() []fleet.Fleet {
			return []fleet.Fleet{{Name: "main", Orchestrator: "sleep001"}, {Name: "busy", Orchestrator: "live0001"}, {Name: "none"}}
		},
		listed: func(_ context.Context, id string) (bool, error) { return alive[id], nil },
		resume: func(id string) error {
			mu.Lock()
			defer mu.Unlock()
			resumed = append(resumed, id)
			return nil
		},
		every: time.Hour,
	}
	for range 2 {
		for _, name := range []string{"main", "busy", "none", "unknown"} {
			w.wake(context.Background(), name)
		}
	}
	if len(resumed) != 1 || resumed[0] != "sleep001" {
		t.Fatalf("resumed %v", resumed)
	}
	// The first fleet is also addressed by the empty name.
	w2 := &orchestratorWaker{fleets: w.fleets, listed: w.listed, resume: func(string) error { return errors.New("gone for good") }, every: time.Hour}
	if err := w2.wake(context.Background(), ""); err == nil {
		t.Fatal("a failed wake must be reported")
	}
}
