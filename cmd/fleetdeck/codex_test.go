package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/codex"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/executor"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
)

func TestExecutorRowsSayOnlyWhatWasObserved(t *testing.T) {
	key := executor.Key("codex:" + runtimeTestThread)
	asks := sessionRow(executor.Session{Key: key, Lifecycle: "live", Activity: "working", NeedsHuman: executor.Yes, Reason: codex.Needs})
	if asks.lifecycle != state.LifecycleLive || asks.session.State != "working" || asks.session.Waiting() != daemon.Yes {
		t.Fatalf("a question must count as waiting: %+v", asks)
	}
	unknown := sessionRow(executor.Session{Key: key, Lifecycle: "live", Activity: "unknown"})
	if unknown.session.State != "" || unknown.session.Needs != nil {
		t.Fatalf("an unobserved session claimed a state: %+v", unknown)
	}
	idle := sessionRow(executor.Session{Key: key, Lifecycle: "live", Activity: "idle", NeedsHuman: executor.No})
	if idle.session.Needs == nil || *idle.session.Needs != "" {
		t.Fatalf("an observed no must read as no: %+v", idle)
	}
	gone := sessionRow(executor.Session{Key: key, Lifecycle: "stopped", Activity: "unknown"})
	if gone.lifecycle != state.LifecycleStopped || gone.lastState != "unknown" || gone.session.State != "" {
		t.Fatalf("stopped row: %+v", gone)
	}
}

func TestCodexIsOffOnAStandWithoutItsOwnHome(t *testing.T) {
	stand := runOpts{configPath: "/stand/config.yaml", standSocket: "/stand/daemon.sock"}
	cfg := config.Config{BoardPath: "/board", Codex: config.CodexConfig{Enabled: true}}
	if codexAllowed(stand, cfg) {
		t.Fatal("a stand would reach the operator's Codex home")
	}
	if a, err := executors(stand, cfg, orchestrator.Claude{}).ForID(executor.Codex); err != nil || a.Start != nil || a.Resume != nil || a.Stop == nil {
		t.Fatalf("a stand without its own home may only stop and read Codex: %v", err)
	}
	if err := resumeVia(executors(stand, cfg, orchestrator.Claude{}))("codex:" + runtimeTestThread); !errors.Is(err, server.ErrSessionNotResumable) {
		t.Fatalf("a stand without its own home resumed Codex: %v", err)
	}
	cfg.Codex.Home = "/stand/codex-home"
	if !codexAllowed(stand, cfg) || codexAllowed(runOpts{configPath: "/c/config.yaml"}, config.Config{}) {
		t.Fatal("codex allowance")
	}
	if got := codexStateDir("/stand/config.yaml"); got != filepath.Join("/stand", "runtime") {
		t.Fatalf("state dir %s", got)
	}
}

func TestSessionKeysReachOnlyTheirOwnExecutor(t *testing.T) {
	var claude []string
	o := runOpts{configPath: filepath.Join(t.TempDir(), "config.yaml")}
	cfg := config.Config{Codex: config.CodexConfig{Enabled: true}}
	reg := executors(o, cfg, orchestrator.Claude{
		Stop: func(_ context.Context, short string) error { claude = append(claude, "stop:"+short); return nil },
		Send: func(_ context.Context, short, _ string) error { claude = append(claude, "send:"+short); return nil },
	})
	stop, send := stopVia(reg), sendVia(reg)
	ctx := context.Background()
	if err := stop(ctx, "abc12345"); err != nil {
		t.Fatal(err)
	}
	if err := send(ctx, "abc12345", "hi"); err != nil {
		t.Fatal(err)
	}
	key := "codex:" + runtimeTestThread
	if stop(ctx, key) == nil || send(ctx, key, "hi") == nil {
		t.Fatal("a Codex key with no runtime reported success")
	}
	if len(claude) != 2 || claude[0] != "stop:abc12345" || claude[1] != "send:abc12345" {
		t.Fatalf("claude saw %v", claude)
	}
	if !startsAny(executors(o, cfg, orchestrator.Claude{})) {
		t.Fatal("a panel with Codex alone must be able to start Codex cards")
	}
	if startsAny(executors(o, config.Config{}, orchestrator.Claude{})) {
		t.Fatal("a panel that starts nothing offered a start")
	}
}

func TestCodexRowsCarryContextAndTheNewestAccountLimits(t *testing.T) {
	key := executor.Key("codex:" + runtimeTestThread)
	old, newer := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)
	limits := func(at time.Time, five float64) *executor.Metric[[]executor.Limit] {
		return &executor.Metric[[]executor.Limit]{ObservedAt: at, Value: []executor.Limit{{Name: "5h", UsedPercent: five}, {Name: "7d", UsedPercent: 51}}}
	}
	a := sessionRow(executor.Session{Key: key, Lifecycle: "live", Activity: "working", Limits: limits(old, 10),
		Context: &executor.Metric[executor.ContextUse]{Value: executor.ContextUse{Tokens: 51200, Window: 256000}}})
	b := sessionRow(executor.Session{Key: "codex:02a1a6d4-90a4-70f3-9130-a59ec873d0ba", Lifecycle: "live", Limits: limits(newer, 20)})
	if a.context == nil || a.context.Tokens != 51200 || a.context.Window != 256000 || a.context.Estimated {
		t.Fatalf("context %+v", a.context)
	}
	got := accountLimits([]listedSession{a, b, {lifecycle: state.LifecycleLive}})
	if got == nil || got.FiveHour.Utilization != 20 || got.SevenDay.Utilization != 51 || !got.FetchedAt.Equal(newer) {
		t.Fatalf("limits %+v", got)
	}
	if accountLimits([]listedSession{{lifecycle: state.LifecycleLive}}) != nil {
		t.Fatal("no reported limits must read as none")
	}
}
