// Package config loads and saves the fleetdeck configuration file.
// A missing file is a set of defaults, not a failure. A malformed file is a failure.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type NotifyConfig struct {
	Waiting      bool          `yaml:"waiting"`
	Failed       bool          `yaml:"failed"`
	Silent       bool          `yaml:"silent"`
	CardBlocked  bool          `yaml:"card_blocked"`
	SilenceAfter time.Duration `yaml:"silence_after"`
}

type Config struct {
	BoardPath           string        `yaml:"board_path"`
	DocsPaths           []string      `yaml:"docs_paths"`
	OrchestratorSession string        `yaml:"orchestrator_session"`
	Notify              NotifyConfig  `yaml:"notify"`
	DaemonPollInterval  time.Duration `yaml:"daemon_poll_interval"`
	UsageEnabled        bool          `yaml:"usage_enabled"`
	ServerPort          int           `yaml:"server_port"`
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		Notify: NotifyConfig{
			Waiting: true, Failed: true, Silent: true, CardBlocked: true,
			SilenceAfter: 30 * time.Minute,
		},
		DaemonPollInterval: 2 * time.Second,
		UsageEnabled:       true,
		ServerPort:         7777,
	}
}

// Load reads the config file. A missing file yields defaults; a malformed one is an error.
func Load(path string) (Config, error) {
	c := Default()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return c, nil
}

// Save writes the config file, creating parent directories as needed.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	raw, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return os.WriteFile(path, raw, 0o600)
}
