package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/executor"
	"github.com/kroticw/fleetdeck/internal/server"
)

func settingsFixture(t *testing.T) (*settingsStore, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("# mine\nboard:\n    path: /b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	cfg.OrchestratorSession = "pinned"
	c := NewCollector(cfg, deadDaemon(t), nil, t.TempDir())
	return &settingsStore{o: runOpts{configPath: p, standSocket: filepath.Join(t.TempDir(), "none.sock")}, collector: c}, p
}

// A write reaches the file and the live configuration at once; what the
// panel keeps in memory per fleet stays.
func TestSettingsAreWrittenAndAppliedAtOnce(t *testing.T) {
	s, p := settingsFixture(t)
	_, err := s.write(context.Background(), map[string]any{
		"workers.effort": "xhigh", "executors.codex.enabled": true, "default_executor": "codex",
		"ui.terminal_font_size": float64(14), "notify.silence_after": "45m", "agent.command": []any{"/opt/claude", "--x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	live := s.collector.Config()
	if live.Workers.Effort != "xhigh" || !live.Codex.Enabled || live.DefaultExecutor != executor.Codex || live.UI.TerminalFontSize != 14 ||
		live.Notify.SilenceAfter != 45*time.Minute || len(live.Agent.Command) != 2 || live.OrchestratorSession != "pinned" {
		t.Fatalf("live = %+v", live)
	}
	raw, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(raw), "# mine") || !strings.Contains(string(raw), "effort: xhigh") {
		t.Fatalf("file:\n%s", raw)
	}
	page, err := s.page(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v := page.(settingsPage).Values
	if v["workers.effort"] != "xhigh" || v["notify.silence_after"] != "45m" || v["daemon.poll_interval"] != "2s" {
		t.Fatalf("values = %v", v)
	}
}

func TestSettingsRefuseUnknownKeysAndBadValues(t *testing.T) {
	s, p := settingsFixture(t)
	before, _ := os.ReadFile(p)
	for _, bad := range []map[string]any{
		{"board.path": "/elsewhere"},
		{"workers.effort": "huge"},
		{"workers.sandbox": "yes"},
		{"server.port": 1.5},
	} {
		if _, err := s.write(context.Background(), bad); !errors.Is(err, server.ErrBadSettings) {
			t.Fatalf("%v: err = %v", bad, err)
		}
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Fatalf("a refused write changed the file:\n%s", after)
	}
}

func TestDurationsReadAsTheyAreWritten(t *testing.T) {
	for d, want := range map[time.Duration]string{2 * time.Second: "2s", 10 * time.Second: "10s", 30 * time.Minute: "30m", 90 * time.Minute: "1h30m", time.Hour: "1h"} {
		if got := durationText(d); got != want {
			t.Errorf("%s = %q, want %q", d, got, want)
		}
	}
}
