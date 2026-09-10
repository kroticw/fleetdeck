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
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// fileMu serialises every read-modify-write-and-atomic-rename cycle this
// package performs against a configuration file: Save, SetField and
// SetSessionLabel. Two independently-triggerable HTTP routes can now write
// the same file at once — the orchestrator pin and a session label, or two
// session labels for different sessions — and without this each writer
// reads the same original bytes and each writes back its own version, one
// silently losing the other's change.
//
// This is an in-process mutex and nothing more: it serialises writers
// within one running fleetdeck, not writers to one config.yaml. Two panels
// pointed at the same file — one operator running several at once on
// different ports, which does happen in practice — can still overwrite
// each other exactly the way a single panel's two concurrent writers used
// to. Fixing that would need an OS-level file lock (flock or equivalent),
// which nothing here provides; this mutex only closes the window between
// goroutines inside one process.
var fileMu sync.Mutex

// NotifyConfig is never serialised directly either — see Config below.
type NotifyConfig struct {
	Waiting     bool
	Failed      bool
	Silent      bool
	CardBlocked bool

	// SilenceAfter is how long a session must be silent before that silence counts
	// as an event at all, and zero turns the silence rule off entirely.
	//
	// It is not a repeat-suppression window: spec line 248 defines
	// notify.silence_after as "сколько сессия должна молчать, чтобы это считалось
	// событием" — a threshold, not a cooldown. No session can be silent for less
	// than no time, so a zero threshold read literally would make every session an
	// event the moment it is first seen and the whole fleet would arrive as banners.
	// Off is the only reading that leaves the setting a way to say "do not call me
	// about silence". validate rejects a negative value outright.
	//
	// The comparison that acts on this lives in state.Diff, as `silenceAfter > 0`,
	// and has to: state applies the rule and must not import this package to ask
	// permission. So this comment is the explanation, not the implementation — there
	// is deliberately no helper here claiming to be the one place the decision is
	// made, because it would be the second one. See docs/en/configuration.md's
	// "What `silence_after: 0` means, and what a negative value does" section for the
	// user-facing statement of the same rule.
	SilenceAfter time.Duration
}

// Config is never serialised directly — Save/Load marshal the nested unexported
// file type below, so Config carries no yaml tags of its own.
type Config struct {
	BoardPath           string
	DocsPaths           []string
	OrchestratorSession string
	// SessionLabels is the operator's own naming for sessions, keyed by the
	// session's transcript UUID (daemon.Session.SessionID) rather than its
	// short id: the short id is the daemon's to reassign, the UUID is not, and
	// a label surviving that reassignment is the whole reason to key on it
	// instead. A session's own name from the daemon (when it has one) already
	// covers the common case; this exists for the sessions that started with
	// no name and cannot be renamed after the fact — see internal/state's
	// SessionView.Label for where a value here reaches the panel, and
	// internal/server's session-label route for how it gets written.
	SessionLabels      map[string]string
	Notify             NotifyConfig
	DaemonPollInterval time.Duration
	UsageEnabled       bool
	ServerPort         int
	// StatuslineWrap is the statusline command cmd/fleetdeck-status passes
	// stdin through to and prints the output of unchanged -- the operator's
	// own tool (spec: their own liked, pre-existing statusline), read from
	// here only when the same-named command-line flag was not given. Empty
	// means no wrapping: fleetdeck-status prints its own plain render.
	StatuslineWrap string
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
//
// This type backs two distinct keys (daemon.poll_interval and notify.silence_after),
// and UnmarshalYAML is never told which one it is decoding — a duration string
// error and the catch-all "wrong shape" error below both carry value.Line instead, so
// the two keys' errors are at least distinguishable from each other, and a
// multi-line config file gets a real pointer to the offending value rather than the
// same fixed sentence twice. The invalid-duration-string branch wraps time.ParseDuration's
// own error directly, rather than re-quoting value.Value itself first: that error already
// names the value (time.ParseDuration's message is `time: invalid duration "…"`), and
// quoting it again ahead of that produced the same value twice in one message.
func (d *configDuration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Tag == "!!str" {
		parsed, err := time.ParseDuration(value.Value)
		if err != nil {
			return fmt.Errorf("line %d: %w", value.Line, err)
		}
		*d = configDuration(parsed)
		return nil
	}
	if value.Kind == yaml.ScalarNode && (value.Tag == "!!int" || value.Tag == "!!float") {
		return fmt.Errorf("must be a duration string like \"30s\", not a bare number (%s)", value.Value)
	}
	return fmt.Errorf("line %d: must be a duration string like \"30s\"", value.Line)
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
	// SessionLabels is a plain map, not a slice of {id, label} pairs: the
	// session UUID is already a unique key, and a map lets SetSessionLabel
	// (internal/config/write.go) find, add, or remove exactly one entry by
	// that key without touching the shape of anything else in the file.
	SessionLabels map[string]string `yaml:"session_labels"`
	Notify        notifyFile        `yaml:"notify"`
	Daemon        struct {
		PollInterval configDuration `yaml:"poll_interval"`
	} `yaml:"daemon"`
	Usage struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"usage"`
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Statusline struct {
		Wrap string `yaml:"wrap"`
	} `yaml:"statusline"`
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

// DefaultPath is where spec section 11 puts the configuration file, exported
// so any command reading it -- cmd/fleetdeck (which has carried this same
// logic locally since before this function existed) and cmd/fleetdeck-status
// -- agree on the one location without each restating it.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "fleetdeck", "config.yaml")
}

// configToFile converts a Config to file format for marshalling.
func configToFile(c Config) file {
	f := file{}
	f.Board.Path = c.BoardPath
	f.Docs.Paths = c.DocsPaths
	f.Orchestrator.Session = c.OrchestratorSession
	f.SessionLabels = c.SessionLabels
	f.Notify.Enabled.Waiting = c.Notify.Waiting
	f.Notify.Enabled.Failed = c.Notify.Failed
	f.Notify.Enabled.Silent = c.Notify.Silent
	f.Notify.Enabled.CardBlocked = c.Notify.CardBlocked
	f.Notify.SilenceAfter = configDuration(c.Notify.SilenceAfter)
	f.Daemon.PollInterval = configDuration(c.DaemonPollInterval)
	f.Usage.Enabled = c.UsageEnabled
	f.Server.Port = c.ServerPort
	f.Statusline.Wrap = c.StatuslineWrap
	return f
}

// fileToConfig converts file format to a Config.
func fileToConfig(f file) Config {
	return Config{
		BoardPath:           f.Board.Path,
		DocsPaths:           f.Docs.Paths,
		OrchestratorSession: f.Orchestrator.Session,
		SessionLabels:       f.SessionLabels,
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
		StatuslineWrap:     f.Statusline.Wrap,
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
		return Config{}, fmt.Errorf("parse config %s: %w", path, describeYAMLError(err))
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

// fieldNotFoundPattern matches yaml.v3's "field <name> not found in type <go type>"
// message, emitted (via KnownFields(true)) for a configuration key this format does
// not recognise. The Go struct type it names, tag literals included, is an
// implementation detail no one hand-editing this YAML file should ever be shown.
var fieldNotFoundPattern = regexp.MustCompile(`^(line \d+: )?field (\S+) not found in type .*$`)

// cannotUnmarshalPattern matches yaml.v3's "cannot unmarshal <tag> [`value`] into
// <target>" message, emitted for a value of the wrong shape — a scalar where a mapping
// was expected, a list where a scalar was expected, and so on. <target> names the exact
// Go type yaml.v3 tried to decode into: for a nested section of this format that is
// either an anonymous struct literal with its yaml tag spelled out verbatim (e.g.
// `struct { Path string "yaml:\"path\"" }`) or one of this package's own private type
// names (e.g. `config.notifyFile`, `config.file`) — captured as the third group below so
// targetLeaksGoSyntax can decide whether this particular occurrence needs rewriting.
var cannotUnmarshalPattern = regexp.MustCompile(`^(line \d+: )?cannot unmarshal (!!\S+)(?: ` + "`[^`]*`" + `)? into (.+)$`)

// targetLeaksGoSyntax reports whether a cannotUnmarshalPattern target names an anonymous
// Go struct literal or one of this package's own private types, as opposed to a plain
// built-in type (int, bool, string, []string, ...) backing a leaf field. A built-in type
// name already reads fine to someone who has never seen this codebase and is left alone;
// only the two Go-specific shapes need rewriting.
func targetLeaksGoSyntax(target string) bool {
	return strings.Contains(target, "struct {") || strings.Contains(target, "config.")
}

// describeYAMLTag turns a yaml.v3 tag (as it appears in a "cannot unmarshal <tag> ..."
// message) into the word a person hand-editing this file would use for what they
// actually wrote, so cannotUnmarshalPattern's rewrite below can say what was found
// without naming the YAML tag syntax either.
//
// !!null and !!map are deliberately absent from this switch: every target this
// rewrite ever fires for (see targetLeaksGoSyntax) is itself a mapping-shaped Go type
// — a struct, named or anonymous — and yaml.v3 decodes a null or a mapping into a
// mapping-shaped target without error, so neither tag can ever reach a "cannot
// unmarshal" message whose target is one of those two shapes. Handling them here would
// be an untestable branch asserting a case that cannot occur; the default below covers
// them, and anything else genuinely unforeseen, honestly instead.
func describeYAMLTag(tag string) string {
	switch tag {
	case "!!str":
		return "a text value"
	case "!!seq":
		return "a list"
	case "!!int", "!!float":
		return "a number"
	case "!!bool":
		return "a true/false value"
	default:
		return "a value of the wrong shape"
	}
}

// describeYAMLError rewrites a *yaml.TypeError's messages in terms of configuration
// keys, never Go syntax. yaml.v3's own text under KnownFields(true) leaks Go internals
// in two distinct shapes, handled by the two patterns above:
//
//   - an unknown key: `field pathx not found in type struct { Path string
//     "yaml:\"path\"" }` — the exact anonymous struct type, tag literal included.
//     fieldNotFoundPattern rewrites this to name the offending key instead.
//   - a value of the wrong shape: `cannot unmarshal !!str `hello` into struct { Path
//     string "yaml:\"path\"" }`, or, for a named private type, `cannot unmarshal !!int
//     `5` into config.notifyFile`. cannotUnmarshalPattern rewrites this — but only when
//     targetLeaksGoSyntax says the target actually is one of those two shapes — to say
//     what was expected (a set of configuration keys) and what was found instead (a
//     text value, a list, a number, ...), naming neither the struct literal nor the
//     package-qualified type.
//
// A cannotUnmarshalPattern match whose target is a plain built-in type (int, bool,
// string, []string — the type actually backing a leaf field) already reads fine to
// someone who has never seen this codebase, and is left as-is. An error that is not a
// *yaml.TypeError at all (a syntax error, an I/O error), or a TypeError line that
// matches neither pattern, is returned unchanged too — this function only ever narrows
// what yaml.v3 already reported, never invents a diagnosis on top of it.
func describeYAMLError(err error) error {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return err
	}

	rewritten := make([]string, len(typeErr.Errors))
	for i, line := range typeErr.Errors {
		if m := fieldNotFoundPattern.FindStringSubmatch(line); m != nil {
			rewritten[i] = fmt.Sprintf("%sunknown configuration key %q", m[1], m[2])
			continue
		}
		if m := cannotUnmarshalPattern.FindStringSubmatch(line); m != nil && targetLeaksGoSyntax(m[3]) {
			rewritten[i] = fmt.Sprintf("%sexpected a set of configuration keys here, not %s", m[1], describeYAMLTag(m[2]))
			continue
		}
		rewritten[i] = line
	}
	return errors.New(strings.Join(rewritten, "\n"))
}

// validate rejects configuration values that would be silently harmful: an out-of-range
// port, a non-positive poll interval that would spin in a hot loop against the daemon
// socket, or a negative silence window that means either "always silent" or "never
// silent" depending on how it is later compared — neither of which is what a negative
// duration was meant to express. Zero is not rejected here: unlike a negative value, it
// has one defined meaning (see NotifyConfig.SilenceAfter) rather than two competing
// ones, so there is nothing for validate to refuse.
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

// sweepStaleTempFiles removes every file directly under dir matching base+".tmp-*" —
// exactly the pattern os.CreateTemp(dir, base+".tmp-*") below produces. It exists for
// the one case Save's own deferred removal cannot cover: a process killed between
// os.CreateTemp and os.Rename in an earlier run leaves that run's temp file behind with
// no code left running to clean it up. Called once at the start of every Save, so a
// leftover never survives past the next successful write. Best-effort throughout: a
// glob or remove failure here must never fail the write this call is nested inside —
// tidying up an old run's litter is not worth refusing to save the current one over.
func sweepStaleTempFiles(dir, base string) {
	matches, err := filepath.Glob(filepath.Join(dir, base+".tmp-*"))
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.Remove(m)
	}
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
	fileMu.Lock()
	defer fileMu.Unlock()

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

	// A previous Save's temp file, from a run that died (a hard kill, not a Go-level
	// error return — the deferred removal below already handles every error-return
	// path of this function) between os.CreateTemp and os.Rename, would otherwise sit
	// beside the config forever: nothing else ever visits it again. Sweep any leftover
	// matching this function's own naming pattern before creating this run's own, so
	// they cannot accumulate release over release. Best-effort: a sweep failure is not
	// this write's problem to solve, and must never fail the write it is here to tidy
	// up around.
	sweepStaleTempFiles(dir, filepath.Base(path))

	f := configToFile(c)
	raw, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	// 0600 rather than whatever an existing file already had: Save may be
	// creating this file for the first time, so there is no prior mode to
	// preserve. SetField, which only ever edits a file that already exists,
	// preserves that file's own mode instead — see its own call to this
	// same helper.
	return writeFileAtomically(path, raw, 0o600)
}
