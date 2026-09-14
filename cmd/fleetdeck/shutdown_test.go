package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
)

// An update stops the panel it replaces, and the old window gives the whole
// handover 2.436 s (T-060). A panel told to stop waits for the collect cycle
// it is in, and that cycle asks the keychain for the usage token through
// `security`, which can take as long as it likes: measured on a stand with a
// `security` that took 1 s, a SIGTERM 50 ms after start waited 1022 ms. The
// cycle is cancelled when the panel is told to stop, and `security` with it.
func TestAPanelToldToStopDuringAKeychainLookupGoesAtOnce(t *testing.T) {
	r := newPanelRig(t)
	cfg := config.Default()
	cfg.ServerPort = r.port
	cfg.UsageEnabled = true
	cfg.BoardPath = filepath.Join(filepath.Dir(r.cfg), "board")
	if err := config.Save(r.cfg, cfg); err != nil {
		t.Fatal(err)
	}

	stubs := t.TempDir()
	asked := filepath.Join(stubs, "security-asked")
	script := "#!/bin/sh\ntouch " + asked + "\nexec sleep 5\n"
	if err := os.WriteFile(filepath.Join(stubs, "security"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	panel := exec.Command(r.bin, "--config", r.cfg, "--stand-socket", r.noDaemon)
	panel.Env = append(os.Environ(), "HOME="+r.home, "PATH="+stubs+":"+os.Getenv("PATH"))
	if err := panel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(panel.Process.Pid, syscall.SIGKILL) })
	exited := make(chan struct{})
	go func() {
		_ = panel.Wait()
		close(exited)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(asked); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the panel never asked the keychain for the usage token")
		}
		time.Sleep(20 * time.Millisecond)
	}

	termed := time.Now()
	_ = panel.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
		if took := time.Since(termed); took > time.Second {
			t.Fatalf("the panel went %s after SIGTERM; want it gone at once, not after the keychain lookup", took.Round(time.Millisecond))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the panel was still running 10 s after SIGTERM")
	}
}

// On an update stand run of 2026-09-14 (T-060) the panel being replaced took
// 2113 ms to close its server, waiting on one connection that had sent no
// request -- a probe of the new window's -- until the old window's deadline
// killed the new window and the connection with it. A connection with a request
// in work is waited on; one that has asked for nothing is closed.
func TestAPanelToldToStopDoesNotWaitOnAConnectionThatAskedForNothing(t *testing.T) {
	r := newPanelRig(t)
	panel := exec.Command(r.bin, "--config", r.cfg, "--stand-socket", r.noDaemon)
	panel.Env = append(os.Environ(), "HOME="+r.home)
	if err := panel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(panel.Process.Pid, syscall.SIGKILL) })
	exited := make(chan struct{})
	go func() {
		_ = panel.Wait()
		close(exited)
	}()
	if !r.waitAnswer(true, 10*time.Second) {
		t.Fatal("the panel never answered")
	}

	held, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", r.port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	// Accepted by the panel, not only by the kernel.
	time.Sleep(200 * time.Millisecond)

	termed := time.Now()
	_ = panel.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
		if took := time.Since(termed); took > time.Second {
			t.Fatalf("the panel went %s after SIGTERM with a connection that asked for nothing; want it gone at once", took.Round(time.Millisecond))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the panel was still running 10 s after SIGTERM")
	}
}
