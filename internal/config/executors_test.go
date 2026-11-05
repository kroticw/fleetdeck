package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/executor"
)

func TestCodexSettingsDoNotInheritClaudeWorkers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("workers:\n  model: opus\n  permission_mode: auto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultExecutor != executor.Claude || c.Codex.Enabled || c.Codex.Model != "" || c.Codex.ApprovalPolicy != "" || c.Codex.Sandbox != "" {
		t.Fatalf("Claude settings reached Codex: %+v", c.Codex)
	}
}

func TestCodexConfigRoundTripAndScalarEditsPreserveIt(t *testing.T) {
	c := Default()
	c.DefaultExecutor = executor.Codex
	c.Codex = CodexConfig{Enabled: true, Command: []string{"/opt/codex wrapper", "codex"}, Home: "/private/codex home", Profile: "work", Model: "model-id", Sandbox: "workspace-write", ApprovalPolicy: "on-request"}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append([]byte("# user note\n"), raw...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetField(p, "orchestrator.session", "abc12345"); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || !reflect.DeepEqual(got.Codex, c.Codex) || got.DefaultExecutor != executor.Codex || got.OrchestratorSession != "abc12345" {
		t.Fatalf("config=%+v err=%v", got, err)
	}
	after, err := os.ReadFile(p)
	if err != nil || !strings.HasPrefix(string(after), "# user note\n") {
		t.Fatalf("user comment lost: %v", err)
	}
}

func TestCodexConfigRejectsInvalidLaunchSettings(t *testing.T) {
	for _, content := range []string{
		"default_executor: other\n",
		"default_executor: codex\n",
		"executors:\n  codex:\n    home: relative\n",
		"executors:\n  codex:\n    command: [/bin/codex, '']\n",
		"executors:\n  codex:\n    command: [./codex]\n",
		"executors:\n  codex:\n    sandbox: full-access\n",
		"executors:\n  codex:\n    approval_policy: auto\n",
	} {
		t.Run(strings.ReplaceAll(content, "\n", "/"), func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p); err == nil {
				t.Fatal("invalid Codex settings accepted")
			}
		})
	}
}
