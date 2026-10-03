package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
)

// claudeThatRecordsConfigDir writes a claude that prints what `claude --bg`
// prints and leaves behind the CLAUDE_CONFIG_DIR it was run with, "unset"
// when it had none — the one input Claude Code picks its installation by.
func claudeThatRecordsConfigDir(t *testing.T) (bin, seen string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "claude")
	seen = filepath.Join(dir, "config-dir")
	script := "#!/bin/sh\n" +
		"if [ \"${CLAUDE_CONFIG_DIR+set}\" = set ]; then printf '%s' \"$CLAUDE_CONFIG_DIR\" > '" + seen + "'; else printf unset > '" + seen + "'; fi\n" +
		"echo 'backgrounded · 44444444 · orchestrator'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, seen
}

// installationOf is the installation a claude run with what seen recorded
// works in, by Claude Code's own rule: CLAUDE_CONFIG_DIR when it is set, else
// ~/.claude, and either way the path resolved — the daemon's runtime directory
// is named after the resolved path, so /x/ and /x are one installation.
func installationOf(t *testing.T, seen, home string) string {
	t.Helper()
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("the claude did not run: %v", err)
	}
	if string(got) == "unset" {
		return filepath.Join(home, ".claude")
	}
	return filepath.Clean(string(got))
}

// The property agent.config_dir rests on: whatever the configuration names and
// whatever the panel's own environment carries, a session the panel starts or
// stops with the claude it finds works in the installation the panel reads —
// the one whose daemon, control key and transcripts are on screen. Started in
// another, it is a session the panel can neither see nor stop.
func TestAFoundClaudeWorksInTheInstallationThePanelReads(t *testing.T) {
	for _, configDir := range []string{"", "/x/.claude-work", "/x/.claude-work/", "/x/./.claude-work"} {
		for _, inherited := range []string{"", "/elsewhere/.claude"} {
			for _, op := range []string{"start", "stop"} {
				name := op + " config_dir=" + configDir + " inherited=" + inherited
				t.Run(name, func(t *testing.T) {
					home := t.TempDir()
					t.Setenv("HOME", home)
					if inherited == "" {
						t.Setenv("CLAUDE_CONFIG_DIR", "")
						_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
					} else {
						t.Setenv("CLAUDE_CONFIG_DIR", inherited)
					}
					bin, seen := claudeThatRecordsConfigDir(t)
					t.Setenv("PATH", filepath.Dir(bin))
					cfg := config.Config{Agent: config.AgentConfig{ConfigDir: configDir}}

					var err error
					if op == "start" {
						_, err = sessionStarter(runOpts{}, cfg.Agent)(context.Background(), t.TempDir(), "orchestrator")
					} else {
						err = sessionStopper(runOpts{}, cfg.Agent)(context.Background(), "abc12345")
					}
					if err != nil {
						t.Fatal(err)
					}
					if got, want := installationOf(t, seen, home), filepath.Clean(claudeDirOf(cfg)); got != want {
						t.Errorf("the session went to %q, the panel reads %q", got, want)
					}
				})
			}
		}
	}
}

// A configured command is a wrapper that picks its installation itself, so it
// is run with the panel's environment as it is: nothing here can tell which
// directory the wrapper means, and overriding it would be a guess.
func TestAConfiguredCommandKeepsThePanelsEnvironment(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/wrapper/decides")
	bin, seen := claudeThatRecordsConfigDir(t)
	agent := config.AgentConfig{Command: []string{bin}, ConfigDir: "/x/.claude-work"}

	if _, err := sessionStarter(runOpts{}, agent)(context.Background(), t.TempDir(), "orchestrator"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(seen); string(got) != "/wrapper/decides" {
		t.Errorf("start: the command saw CLAUDE_CONFIG_DIR %q, want the panel's own", got)
	}
	if err := sessionStopper(runOpts{}, agent)(context.Background(), "abc12345"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(seen); string(got) != "/wrapper/decides" {
		t.Errorf("stop: the command saw CLAUDE_CONFIG_DIR %q, want the panel's own", got)
	}
}

// claudeEnv drops every inherited CLAUDE_CONFIG_DIR, not only the first: an
// environment can carry a name twice, and exec hands the child the last one.
func TestClaudeEnvLeavesNoInheritedConfigDir(t *testing.T) {
	env := claudeEnv([]string{"A=1", "CLAUDE_CONFIG_DIR=/one", "CLAUDE_CONFIG_DIR=/two", "B=2"}, "")
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			t.Errorf("env still carries %q", kv)
		}
	}
	if len(env) != 2 {
		t.Errorf("env = %q, want the other two variables kept", env)
	}
}
