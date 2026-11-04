package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kroticw/fleetdeck/internal/executor"
)

// CodexConfig is independent of Agent and Workers, which configure Claude.
// Empty model/profile/policies preserve Codex's own configuration defaults.
type CodexConfig struct {
	Enabled        bool     `yaml:"enabled,omitempty"`
	Command        []string `yaml:"command,omitempty"`
	Home           string   `yaml:"home,omitempty"`
	Profile        string   `yaml:"profile,omitempty"`
	Model          string   `yaml:"model,omitempty"`
	Sandbox        string   `yaml:"sandbox,omitempty"`
	ApprovalPolicy string   `yaml:"approval_policy,omitempty"`
}

func validateExecutors(c Config) error {
	id, err := executor.ParseID(string(c.DefaultExecutor))
	if err != nil {
		return fmt.Errorf("default_executor: %w", err)
	}
	if id == executor.Codex && !c.Codex.Enabled {
		return errors.New("default_executor codex requires executors.codex.enabled")
	}
	return c.Codex.Validate()
}

// Validate is also called by the launcher before it constructs argv.
func (c CodexConfig) Validate() error {
	if c.Home != "" && (!filepath.IsAbs(c.Home) || strings.ContainsRune(c.Home, 0)) {
		return errors.New("executors.codex.home must be an absolute path")
	}
	for _, arg := range c.Command {
		if strings.TrimSpace(arg) == "" || strings.ContainsRune(arg, 0) {
			return errors.New("executors.codex.command contains an empty or invalid argument")
		}
	}
	if len(c.Command) > 0 && !filepath.IsAbs(c.Command[0]) {
		return errors.New("executors.codex.command must start with an absolute executable path")
	}
	if c.Sandbox != "" && !slices.Contains([]string{"read-only", "workspace-write", "danger-full-access"}, c.Sandbox) {
		return errors.New("executors.codex.sandbox must be read-only, workspace-write or danger-full-access")
	}
	// These are the TUI's --ask-for-approval values in the supported CLI.
	if c.ApprovalPolicy != "" && !slices.Contains([]string{"on-request", "never"}, c.ApprovalPolicy) {
		return errors.New("executors.codex.approval_policy must be on-request or never")
	}
	for _, v := range []string{c.Profile, c.Model} {
		if strings.ContainsAny(v, "\x00\r\n") {
			return errors.New("executors.codex profile and model must be single-line values")
		}
	}
	return nil
}
