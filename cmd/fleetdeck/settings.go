package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/userenv"
)

// settingKind is how a setting's value is carried in JSON and written.
type settingKind int

const (
	kindString settingKind = iota
	kindBool
	kindInt
	kindList
)

// setting is one key the settings page may write. restart is whether it takes
// effect only at the panel's next start: everything the panel reads once, at
// start, rather than from its live configuration.
type setting struct {
	kind    settingKind
	restart bool
}

// settings are the keys the settings page writes. Per-fleet keys (board,
// docs, orchestrator) and session labels have their own places and are not
// here.
var settings = map[string]setting{
	"workers.model":                   {kindString, false},
	"workers.effort":                  {kindString, false},
	"workers.permission_mode":         {kindString, false},
	"workers.sandbox":                 {kindBool, false},
	"default_executor":                {kindString, false},
	"executors.codex.enabled":         {kindBool, false},
	"executors.codex.model":           {kindString, false},
	"executors.codex.effort":          {kindString, false},
	"executors.codex.sandbox":         {kindString, false},
	"executors.codex.approval_policy": {kindString, false},
	"executors.codex.home":            {kindString, false},
	"executors.codex.profile":         {kindString, false},
	"notify.enabled.waiting":          {kindBool, true},
	"notify.enabled.failed":           {kindBool, true},
	"notify.enabled.silent":           {kindBool, true},
	"notify.enabled.card_blocked":     {kindBool, true},
	"notify.silence_after":            {kindString, true},
	"usage.enabled":                   {kindBool, false},
	"server.port":                     {kindInt, true},
	"daemon.poll_interval":            {kindString, true},
	"agent.command":                   {kindList, true},
	"agent.config_dir":                {kindString, true},
	"statusline.wrap":                 {kindString, true},
	"statusline.rate_limits_path":     {kindString, true},
	"ui.theme":                        {kindString, false},
	"ui.terminal_font_size":           {kindInt, false},
}

// settingValues is c as the settings page reads it, one value per key.
func settingValues(c config.Config) map[string]any {
	return map[string]any{
		"workers.model":                   c.Workers.Model,
		"workers.effort":                  c.Workers.Effort,
		"workers.permission_mode":         c.Workers.PermissionMode,
		"workers.sandbox":                 c.Workers.Sandbox,
		"default_executor":                string(c.DefaultExecutor),
		"executors.codex.enabled":         c.Codex.Enabled,
		"executors.codex.model":           c.Codex.Model,
		"executors.codex.effort":          c.Codex.Effort,
		"executors.codex.sandbox":         c.Codex.Sandbox,
		"executors.codex.approval_policy": c.Codex.ApprovalPolicy,
		"executors.codex.home":            c.Codex.Home,
		"executors.codex.profile":         c.Codex.Profile,
		"notify.enabled.waiting":          c.Notify.Waiting,
		"notify.enabled.failed":           c.Notify.Failed,
		"notify.enabled.silent":           c.Notify.Silent,
		"notify.enabled.card_blocked":     c.Notify.CardBlocked,
		"notify.silence_after":            durationText(c.Notify.SilenceAfter),
		"usage.enabled":                   c.UsageEnabled,
		"server.port":                     c.ServerPort,
		"daemon.poll_interval":            durationText(c.DaemonPollInterval),
		"agent.command":                   slices.Clone(c.Agent.Command),
		"agent.config_dir":                c.Agent.ConfigDir,
		"statusline.wrap":                 c.StatuslineWrap,
		"statusline.rate_limits_path":     c.StatuslineRateLimitsPath,
		"ui.theme":                        c.UI.Theme,
		"ui.terminal_font_size":           c.UI.TerminalFontSize,
	}
}

// durationText is d as a config file writes it: "30m", not "30m0s".
func durationText(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// settingsPage is what GET /api/settings answers.
type settingsPage struct {
	Values  map[string]any `json:"values"`
	Restart []string       `json:"restart"`
	Claude  claudeChoices  `json:"claude"`
	Codex   codexChoices   `json:"codex"`
}

type claudeChoices struct {
	Version         string   `json:"version,omitempty"`
	VersionError    string   `json:"versionError,omitempty"`
	DefaultModel    string   `json:"defaultModel"`
	Models          []string `json:"models"`
	Efforts         []string `json:"efforts"`
	DefaultMode     string   `json:"defaultPermissionMode"`
	PermissionModes []string `json:"permissionModes"`
}

type codexChoices struct {
	Version      string               `json:"version,omitempty"`
	VersionError string               `json:"versionError,omitempty"`
	Models       []codexruntime.Model `json:"models"`
	ModelsError  string               `json:"modelsError,omitempty"`
}

// claudeModels are the aliases claude's --model takes for the latest model of
// each family (2.1.316); a full model name is typed in instead.
var claudeModels = []string{"fable", "opus", "sonnet", "haiku"}

// settingsStore is the settings page for one panel: its file, its live
// configuration, and the CLIs it asks for their versions and models.
type settingsStore struct {
	o         runOpts
	collector *Collector
	versions  versionCache
}

func (s *settingsStore) page(ctx context.Context) (any, error) {
	cfg := s.collector.Config()
	restart := []string{}
	for key, st := range settings {
		if st.restart {
			restart = append(restart, key)
		}
	}
	slices.Sort(restart)
	page := settingsPage{
		Values:  settingValues(cfg),
		Restart: restart,
		Claude: claudeChoices{
			DefaultModel: orchestrator.DefaultModel, Models: claudeModels, Efforts: config.Efforts,
			DefaultMode: orchestrator.DefaultPermissionMode, PermissionModes: config.PermissionModes,
		},
		Codex: codexChoices{Models: []codexruntime.Model{}},
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		page.Claude.Version, page.Claude.VersionError = s.versions.of(ctx, s.claudeArgv(cfg))
	})
	wg.Go(func() {
		page.Codex.Version, page.Codex.VersionError = s.versions.of(ctx, codexArgv(cfg.Codex))
	})
	if codexAllowed(s.o, cfg) {
		wg.Go(func() {
			home, err := codexHome(cfg.Codex, userenv.Environ())
			if err == nil {
				lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				var models []codexruntime.Model
				if models, err = codexruntime.ListModels(lctx, filepath.Join(home, "app-server-control", "app-server-control.sock")); err == nil {
					page.Codex.Models = models
					return
				}
			}
			page.Codex.ModelsError = "Codex's model list is not available (" + err.Error() + "); its app-server starts with the first Codex session"
		})
	}
	wg.Wait()
	return page, nil
}

// write writes values to the file, then takes the file back as the live
// configuration, so what applies without a restart applies now.
func (s *settingsStore) write(ctx context.Context, values map[string]any) (any, error) {
	clean := make(map[string]any, len(values))
	for key, v := range values {
		st, ok := settings[key]
		if !ok {
			return nil, fmt.Errorf("%w: %s is not a setting this page writes", server.ErrBadSettings, key)
		}
		cv, err := settingValue(st.kind, v)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", server.ErrBadSettings, key, err)
		}
		clean[key] = cv
	}
	if err := config.SetValues(s.o.configPath, clean); err != nil {
		return nil, fmt.Errorf("%w: %w", server.ErrBadSettings, err)
	}
	cfg, err := config.Load(s.o.configPath)
	if err != nil {
		return nil, err
	}
	s.collector.ApplySettings(cfg)
	return s.page(ctx)
}

// settingValue is v, decoded from JSON, as the kind the key holds.
func settingValue(kind settingKind, v any) (any, error) {
	switch kind {
	case kindString:
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s), nil
		}
	case kindBool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case kindInt:
		if f, ok := v.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1<<31 {
			return int(f), nil
		}
	case kindList:
		items, ok := v.([]any)
		if !ok {
			break
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("a list of strings is expected")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%v is not a value of this setting", v)
}

// claudeArgv is the claude the panel starts sessions with, asked for its
// version: the stand's, the configured command, or the one found on PATH.
func (s *settingsStore) claudeArgv(cfg config.Config) []string {
	switch {
	case s.o.standSocket != "":
		if s.o.standClaude == "" {
			return nil
		}
		return []string{s.o.standClaude, "--version"}
	case len(cfg.Agent.Command) > 0:
		return append(slices.Clone(cfg.Agent.Command), "--version")
	}
	home, _ := os.UserHomeDir()
	bin, err := orchestrator.FindClaude(home, exec.LookPath, claudePlaces)
	if err != nil {
		return nil
	}
	return []string{bin, "--version"}
}

// codexArgv is the codex the runtimes start, asked for its version.
func codexArgv(c config.CodexConfig) []string {
	if len(c.Command) > 0 {
		return append(slices.Clone(c.Command), "--version")
	}
	bin, err := exec.LookPath("codex")
	if err != nil {
		return nil
	}
	return []string{bin, "--version"}
}

// versionCache keeps each CLI's answer for a while: the page is opened often,
// and a CLI is upgraded rarely.
type versionCache struct {
	mu      sync.Mutex
	answers map[string]versionAnswer
}

type versionAnswer struct {
	version, err string
	at           time.Time
}

const versionTTL = 10 * time.Minute

func (v *versionCache) of(ctx context.Context, argv []string) (string, string) {
	if len(argv) == 0 {
		return "", "not found"
	}
	key := strings.Join(argv, "\x00")
	v.mu.Lock()
	if a, ok := v.answers[key]; ok && time.Since(a.at) < versionTTL {
		v.mu.Unlock()
		return a.version, a.err
	}
	v.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = userenv.Environ()
	out, err := cmd.Output()
	a := versionAnswer{at: time.Now()}
	if err != nil {
		a.err = err.Error()
	} else {
		a.version = versionNumber(string(out))
	}
	v.mu.Lock()
	if v.answers == nil {
		v.answers = map[string]versionAnswer{}
	}
	v.answers[key] = a
	v.mu.Unlock()
	return a.version, a.err
}

// versionNumber is the first word of a --version line that starts with a
// digit: "2.1.316 (Claude Code)" and "codex-cli 0.166.0" both give the number.
func versionNumber(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	for _, word := range strings.Fields(line) {
		if word != "" && word[0] >= '0' && word[0] <= '9' {
			return word
		}
	}
	return strings.TrimSpace(line)
}
