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

// configDuration is time.Duration decoded from YAML with its own validation-shaped
// error for a value that is not a duration string at all, rather than yaml.v3's
// default type-mismatch text for a bare number (e.g. "cannot unmarshal !!int 0 into
// time.Duration" for `poll_interval: 0`, as opposed to the intended `poll_interval: 0s`
// reaching validate's own "must be positive" message). yaml.v3 special-cases the plain
// time.Duration type to marshal and unmarshal it as a duration string already; using a
// distinct named type here means that built-in support no longer applies, so both
// directions are implemented explicitly below rather than relying on it silently
// continuing to work for a type it was never written for.
type configDuration time.Duration

// MarshalYAML writes d as the same duration-string form (e.g. "30s") the plain
// time.Duration type would have produced, so Save's output format does not change.
func (d configDuration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// UnmarshalYAML accepts only a duration string (e.g. "30s"), parsed with
// time.ParseDuration. Anything else — most commonly a bare number, the config file's
// most likely typo for a duration field — is refused here with a message that says so,
// rather than surfacing yaml.v3's default "cannot unmarshal !!int ... into
// time.Duration", which names the Go type involved rather than the fix.
func (d *configDuration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Tag == "!!str" {
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", value.Value, err)
		}
		*d = configDuration(parsed)
		return nil
	}
	if value.Kind == yaml.ScalarNode && (value.Tag == "!!int" || value.Tag == "!!float") {
		return fmt.Errorf("must be a duration string like \"30s\", not a bare number (%s)", value.Value)
	}
	return errors.New("must be a duration string like \"30s\"")
}

// notifyFile represents the nested notify section in the config file.
type notifyFile struct {
	Enabled struct {
		Waiting     bool `yaml:"waiting"`
		Failed      bool `yaml:"failed"`
		Silent      bool `yaml:"silent"`
		CardBlocked bool `yaml:"card_blocked"`
	} `yaml:"enabled"`
	SilenceAfter configDuration `yaml:"silence_after"`
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
		PollInterval configDuration `yaml:"poll_interval"`
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
	f.Notify.SilenceAfter = configDuration(c.Notify.SilenceAfter)
	f.Daemon.PollInterval = configDuration(c.DaemonPollInterval)
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
			SilenceAfter: time.Duration(f.Notify.SilenceAfter),
		},
		DaemonPollInterval: time.Duration(f.Daemon.PollInterval),
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
// port, a non-positive poll interval that would spin in a hot loop against the daemon
// socket, or a negative silence window that means either "always silent" or "never
// silent" depending on how it is later compared — neither of which is what a negative
// duration was meant to express.
func validate(c Config) error {
	if c.ServerPort < 1 || c.ServerPort > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.ServerPort)
	}
	if c.DaemonPollInterval <= 0 {
		return fmt.Errorf("daemon.poll_interval must be positive, got %s", c.DaemonPollInterval)
	}
	if c.Notify.SilenceAfter < 0 {
		return fmt.Errorf("notify.silence_after must not be negative, got %s", c.Notify.SilenceAfter)
	}
	return nil
}

// missingAncestorDirs returns, ordered from the outermost missing ancestor down to dir
// itself, every directory in dir's chain that does not exist yet — exactly the set
// os.MkdirAll(dir, ...) is about to create. It stops at the first ancestor that already
// exists, or whose existence cannot be determined at all: either way, that ancestor,
// and everything above it, is not something this call is about to create, so it must
// never be included. "Does not exist" means exactly errors.Is(err, fs.ErrNotExist) —
// any other Stat failure (EACCES from a restrictive parent, ELOOP from a symlink
// cycle, ENOTDIR from a non-directory earlier in the path, ...) leaves existence
// merely unknown, not confirmed absent, and must not be treated as the latter: an
// ancestor that already exists but happens to be unstat'able must never join this list
// and later get os.Chmod(d, 0o700) from Save (see Save's own comment on why that would
// be wrong).
func missingAncestorDirs(dir string) []string {
	var missing []string
	for p := dir; ; {
		_, err := os.Stat(p)
		if err == nil || !errors.Is(err, fs.ErrNotExist) {
			break
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	// Reverse into outermost-first order, so the caller can chmod a parent before its
	// child if it ever needs to (chmod itself does not require that ordering, but
	// producing it here means a future caller does not have to re-derive it).
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
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
	// Recorded before MkdirAll runs, not after: this is the only way to tell which
	// levels MkdirAll is about to create itself, as opposed to ones that already
	// existed and that Save therefore has no business touching (see below).
	created := missingAncestorDirs(dir)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	// MkdirAll's mode argument is subject to umask, so a directory it just created
	// might not actually end up as 0700 without an explicit chmod — and that applies
	// to every level MkdirAll creates in this one call, not only the last: a
	// multi-level create (e.g. --config a/b/c/config.yaml where only a exists)
	// creates both b and c, and both need the same explicit guarantee. This chmod
	// must only apply to a directory this call created itself: dir's path comes from
	// outside (the --config flag), so unconditionally chmod'ing whatever directory it
	// resolves to — as this used to do — would silently tighten a directory Save has
	// no business touching (e.g. $HOME, the first time a caller points --config at a
	// file directly inside it).
	for _, d := range created {
		if err := os.Chmod(d, 0o700); err != nil {
			return fmt.Errorf("set config dir permissions: %w", err)
		}
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
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set config file permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
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
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}

	return nil
}
