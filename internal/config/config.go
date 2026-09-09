// Package config loads and saves the fleetdeck configuration file.
// A missing file is a set of defaults, not a failure. A malformed file is a failure.
package config

import (
	"bytes"
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

// notifyFile represents the nested notify section in the config file.
type notifyFile struct {
	Enabled struct {
		Waiting     bool `yaml:"waiting"`
		Failed      bool `yaml:"failed"`
		Silent      bool `yaml:"silent"`
		CardBlocked bool `yaml:"card_blocked"`
	} `yaml:"enabled"`
	SilenceAfter time.Duration `yaml:"silence_after"`
}

// file represents the nested structure of the config file.
type file struct {
	Board struct {
		Path string `yaml:"path"`
	} `yaml:"board"`
	Docs struct {
		Paths []string `yaml:"paths"`
	} `yaml:"docs"`
	Orchestrator struct {
		Session string `yaml:"session"`
	} `yaml:"orchestrator"`
	Notify notifyFile `yaml:"notify"`
	Daemon struct {
		PollInterval time.Duration `yaml:"poll_interval"`
	} `yaml:"daemon"`
	Usage struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"usage"`
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
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

// configToFile converts a Config to file format for marshalling.
func configToFile(c Config) file {
	f := file{}
	f.Board.Path = c.BoardPath
	f.Docs.Paths = c.DocsPaths
	f.Orchestrator.Session = c.OrchestratorSession
	f.Notify.Enabled.Waiting = c.Notify.Waiting
	f.Notify.Enabled.Failed = c.Notify.Failed
	f.Notify.Enabled.Silent = c.Notify.Silent
	f.Notify.Enabled.CardBlocked = c.Notify.CardBlocked
	f.Notify.SilenceAfter = c.Notify.SilenceAfter
	f.Daemon.PollInterval = c.DaemonPollInterval
	f.Usage.Enabled = c.UsageEnabled
	f.Server.Port = c.ServerPort
	return f
}

// fileToConfig converts file format to a Config.
func fileToConfig(f file) Config {
	return Config{
		BoardPath:           f.Board.Path,
		DocsPaths:           f.Docs.Paths,
		OrchestratorSession: f.Orchestrator.Session,
		Notify: NotifyConfig{
			Waiting:      f.Notify.Enabled.Waiting,
			Failed:       f.Notify.Enabled.Failed,
			Silent:       f.Notify.Enabled.Silent,
			CardBlocked:  f.Notify.Enabled.CardBlocked,
			SilenceAfter: f.Notify.SilenceAfter,
		},
		DaemonPollInterval: f.Daemon.PollInterval,
		UsageEnabled:       f.Usage.Enabled,
		ServerPort:         f.Server.Port,
	}
}

// Load reads the config file. A missing file yields defaults; a malformed one is an error.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	f := configToFile(Default())
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&f); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return fileToConfig(f), nil
}

// Save writes the config file, creating parent directories as needed.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f := configToFile(c)
	raw, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return os.WriteFile(path, raw, 0o600)
}
