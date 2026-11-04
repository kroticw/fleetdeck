package main

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/executor"
)

const runtimeTestThread = "01a1a6d4-90a4-70f3-9130-a59ec873d0ba"

func TestCodexServiceUsesConfiguredHomeAndOrdinaryResume(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex-home")
	c := config.CodexConfig{Enabled: true, Command: []string{"/opt/codex-wrapper"}, Home: home, Model: "gpt-test", Sandbox: "read-only", ApprovalPolicy: "on-request"}
	spec, err := codexService(c, "/state", "codex:"+runtimeTestThread, []string{"PATH=/usr/bin", "CODEX_HOME=/real/home"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.NativeSocket != filepath.Join(home, "app-server-control", "app-server-control.sock") {
		t.Fatalf("native socket outside the configured home: %s", spec.NativeSocket)
	}
	if spec.Thread["model"] != "gpt-test" || spec.Thread["sandbox"] != "read-only" || spec.Thread["approvalPolicy"] != "on-request" {
		t.Fatalf("thread params: %v", spec.Thread)
	}
	argv, env, err := spec.Launch(runtimeTestThread)
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != "/opt/codex-wrapper" || !slices.Equal(argv[len(argv)-2:], []string{"resume", runtimeTestThread}) || slices.Contains(argv, "--remote") {
		t.Fatalf("argv: %v", argv)
	}
	if !slices.Contains(env, "CODEX_HOME="+home) || slices.Contains(env, "CODEX_HOME=/real/home") {
		t.Fatalf("env does not isolate CODEX_HOME: %v", env)
	}
	if got := codexDaemonArgv(c, ""); !slices.Equal(got, []string{"/opt/codex-wrapper", "app-server", "daemon", "start"}) {
		t.Fatalf("daemon argv: %v", got)
	}
}

func TestCodexServiceRefusesDisabledCodex(t *testing.T) {
	if _, err := codexService(config.CodexConfig{}, "/state", "codex:"+runtimeTestThread, nil); !errors.Is(err, executor.ErrUnavailable) {
		t.Fatalf("disabled Codex: %v", err)
	}
}

func TestRuntimeCommandNeedsKeyAndStateDir(t *testing.T) {
	if err := runtimeCommand([]string{"--key", "codex:" + runtimeTestThread}); err == nil {
		t.Fatal("runtime started without a state directory")
	}
	if err := runtimeCommand([]string{"--state-dir", t.TempDir()}); err == nil {
		t.Fatal("runtime started without a key")
	}
}
