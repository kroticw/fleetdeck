package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestAgentSectionNamesACommandAndAConfigDir(t *testing.T) {
	path := writeConfig(t, `board:
  path: /tmp/board
agent:
  command: [/opt/wrapper, run, claude]
  config_dir: /home/someone/.claude-second
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(cfg.Agent.Command, " "); got != "/opt/wrapper run claude" {
		t.Errorf("command = %q, want %q", got, "/opt/wrapper run claude")
	}
	if cfg.Agent.ConfigDir != "/home/someone/.claude-second" {
		t.Errorf("config_dir = %q", cfg.Agent.ConfigDir)
	}
}

// Saying nothing is the whole existing fleet: an unset agent section means the claude
// on PATH and ~/.claude, exactly as before this section existed.
func TestAgentSectionIsUnsetByDefault(t *testing.T) {
	cfg := Default()
	if len(cfg.Agent.Command) != 0 || cfg.Agent.ConfigDir != "" {
		t.Fatalf("default agent = %+v, want nothing set", cfg.Agent)
	}

	loaded, err := Load(writeConfig(t, "board:\n  path: /tmp/board\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Agent.Command) != 0 || loaded.Agent.ConfigDir != "" {
		t.Fatalf("agent = %+v for a file that does not mention it", loaded.Agent)
	}
}

// A relative path here would be resolved against whatever directory the panel happens
// to have been launched in, and the daemon's socket directory is named after the path
// as written — so the same configuration would find a daemon from a terminal and none
// from the Dock. Refused at load rather than debugged later.
func TestAgentConfigDirMustBeAbsolute(t *testing.T) {
	_, err := Load(writeConfig(t, "board:\n  path: /tmp/board\nagent:\n  config_dir: .claude\n"))
	if err == nil {
		t.Fatal("a relative config_dir was accepted")
	}
	if !strings.Contains(err.Error(), "agent.config_dir") {
		t.Errorf("err = %v, want it to name agent.config_dir", err)
	}
}

// ~ is not expanded anywhere in this file's paths, and a home-relative path would
// silently become a directory literally called "~" — the error says so instead.
func TestAgentConfigDirRefusesATildePath(t *testing.T) {
	_, err := Load(writeConfig(t, "board:\n  path: /tmp/board\nagent:\n  config_dir: ~/.claude\n"))
	if err == nil {
		t.Fatal("a tilde config_dir was accepted")
	}
}

// A panel launched from the Dock has PATH=/usr/bin:/bin:/usr/sbin:/sbin and nothing
// else, so a command named by its bare name is found from a terminal and not found
// from the Dock — and only when a session is started, minutes after the panel came up
// looking healthy. Refused at load, where the message can say what to write instead.
func TestAgentCommandMustBeAnAbsolutePath(t *testing.T) {
	_, err := Load(writeConfig(t, "board:\n  path: /tmp/board\nagent:\n  command: [wrapper, run, claude]\n"))
	if err == nil {
		t.Fatal("a bare command name was accepted")
	}
	if !strings.Contains(err.Error(), "agent.command") {
		t.Errorf("err = %v, want it to name agent.command", err)
	}
}

func TestAgentSectionSurvivesASaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	cfg.BoardPath = "/tmp/board"
	cfg.Agent = AgentConfig{
		Command:   []string{"/opt/wrapper", "run", "claude"},
		ConfigDir: "/home/someone/.claude-second",
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Join(loaded.Agent.Command, " ") != "/opt/wrapper run claude" || loaded.Agent.ConfigDir != "/home/someone/.claude-second" {
		t.Fatalf("round trip lost the agent section: %+v", loaded.Agent)
	}
}
