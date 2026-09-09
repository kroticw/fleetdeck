// Package config loads and saves the fleetdeck configuration file.
// A missing file is a set of defaults, not a failure. A malformed file is a failure.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// NotifyConfig is never serialised directly either — see Config below.
type NotifyConfig struct {
	Waiting      bool
	Failed       bool
	Silent       bool
	CardBlocked  bool
	SilenceAfter time.Duration
}

// Config is never serialised directly — Save/Load marshal the nested unexported
// file type below, so Config carries no yaml tags of its own.
type Config struct {
	BoardPath           string
	DocsPaths           []string
	OrchestratorSession string
	Notify              NotifyConfig
	DaemonPollInterval  time.Duration
	UsageEnabled        bool
	ServerPort          int
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

// Load reads the config file. A missing file yields defaults; so does a file that
// decodes to no document at all — empty, or containing only comments. Either is an
// absence of settings, not a failure. A malformed file is still an error.
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
		if errors.Is(err, io.EOF) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	// A config file must be a single YAML document. A second document after a "---"
	// separator is silently ignored by decoder.Decode above if we stop here — far more
	// likely to be a mistake (a leftover block from editing, a botched merge) than
	// intentional multi-document YAML, which this format was never designed to carry.
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return Config{}, fmt.Errorf("parse config %s: file contains more than one YAML document", path)
	} else if !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}

	c := fileToConfig(f)
	if err := validate(c); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return c, nil
}

// validate rejects configuration values that would be silently harmful: an out-of-range
// port, or a non-positive poll interval that would spin in a hot loop against the
// daemon socket.
func validate(c Config) error {
	if c.ServerPort < 1 || c.ServerPort > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.ServerPort)
	}
	if c.DaemonPollInterval <= 0 {
		return fmt.Errorf("daemon.poll_interval must be positive, got %s", c.DaemonPollInterval)
	}
	return nil
}

// Save writes the config file, creating parent directories as needed.
//
// It validates before writing anything, so it can never leave behind a config that
// Load would refuse — the UI writes here whenever the user toggles notifications or
// pins the orchestrator session, so a bad write would be a live way to brick the next
// startup. The write itself goes through a temporary file in the same directory
// followed by a rename, so a write interrupted by a killed process leaves the previous
// file intact rather than a truncated one: rename is atomic on the same filesystem,
// which a same-directory temp file guarantees. The temp file is fsynced before the
// rename (and the directory fsynced, best-effort, after it) so that guarantee also
// holds across a crash or power loss, not only a killed process: without the sync, the
// rename can reach disk before the data it points to does.
func Save(path string, c Config) error {
	if err := validate(c); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	f := configToFile(c)
	raw, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("set config file permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}

	// Fsync the directory too, so the rename entry itself is durable across a crash,
	// not just the file's contents. This is best-effort: not every platform supports
	// syncing a directory handle, and the rename has already succeeded and is readable
	// either way — only the crash-durability guarantee would be weaker without it.
	if dir, err := os.Open(dir); err == nil {
		_ = dir.Sync()
		dir.Close()
	}

	return nil
}
