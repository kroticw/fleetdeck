package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
)

const (
	// statusBinaryName is the reporter Claude Code runs for every session. init
	// looks for it beside the fleetdeck binary it was started from.
	statusBinaryName = "fleetdeck-status"

	// launchAgentLabel is the launchd label of the panel's agent, and the string
	// that identifies an agent file as one this command wrote.
	launchAgentLabel = "dev.fleetdeck.panel"
	launchAgentFile  = launchAgentLabel + ".plist"

	// exampleCardName is the one card init writes into a board that has nothing in
	// it. An empty board directory is an error to internal/board.Scan, so a new
	// operator who is given an empty directory is given a panel that reports a
	// broken board.
	exampleCardName = "example.md"

	dateLayout = "2006-01-02"
)

// initCommand parses the flags of `fleetdeck init` and runs it against this
// machine's own home directory and this binary's own location.
func initCommand(args []string) error {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	boardPath := flags.String("board", "", "board directory to record in the configuration file this command creates")
	force := flags.Bool("force", false, "replace a statusline or a launch agent that init would otherwise refuse to touch")
	if err := flags.Parse(args); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate own binary: %w", err)
	}
	return runInit(initEnv{home: home, binary: binary, board: *boardPath, force: *force, out: os.Stdout})
}

// initEnv is everything runInit is allowed to touch. It is a struct, and every
// path in it is passed in rather than read from the environment inside, so the
// tests can run the whole command against a temporary home instead of against the
// machine the tests are running on.
type initEnv struct {
	// home is the directory holding .config, .claude and Library.
	home string
	// binary is the path of the running fleetdeck binary; the statusline reporter
	// and the launch agent's program are both derived from it.
	binary string
	// board is the --board flag: empty means "decide for me".
	board string
	// force allows the two steps that would otherwise refuse to touch something the
	// operator configured themselves.
	force bool
	out   io.Writer
}

// initStep is what one step of init did, or refused to do. A refusal carries an
// error and does not stop the steps after it: they are independent of each other,
// and a machine where three of four steps can be done is better served by doing
// three of them and saying so than by doing none.
type initStep struct {
	name string
	note string
	// detail is a second line about the same step, indented under it. It carries
	// what the operator would otherwise have to discover by reading the file
	// afterwards.
	detail string
	err    error
}

// runInit performs every step of `fleetdeck init` and prints what each one did.
// The printed output is the whole user interface of this command, so a step that
// refuses says what it found, what it would have written, and how to proceed.
func runInit(env initEnv) error {
	out := env.out
	if out == nil {
		out = os.Stdout
	}

	cfgPath := filepath.Join(env.home, ".config", "fleetdeck", "config.yaml")
	cfg, cfgCreated, cfgStep := ensureConfig(cfgPath, env)
	steps := []initStep{
		cfgStep,
		ensureBoard(cfgPath, cfg, cfgCreated, cfgStep.err, env),
		ensureStatusline(env),
		ensureLaunchAgent(env),
	}

	failed := 0
	var report strings.Builder
	for _, s := range steps {
		if s.err != nil {
			failed++
			fmt.Fprintf(&report, "%-14s skipped: %v\n", s.name+":", s.err)
			continue
		}
		fmt.Fprintf(&report, "%-14s %s\n", s.name+":", s.note)
		if s.detail != "" {
			fmt.Fprintf(&report, "%-14s %s\n", "", s.detail)
		}
	}

	// Loading the agent is a change to a machine's login behaviour, and it is the
	// operator's to make: init prints the command and does not run it.
	//
	// bootstrap, not load: man launchctl lists load under LEGACY SUBCOMMANDS and
	// names bootstrap among its recommended replacements. This line is the one
	// instruction an operator copies verbatim, so it carries the current spelling.
	// $(id -u) is left for the shell to expand — gui/<uid> is how the same man page
	// spells the target for a user's GUI domain.
	if steps[len(steps)-1].err == nil {
		fmt.Fprintf(&report, "\nstart the panel at login with:\n  launchctl bootstrap gui/$(id -u) %s\n",
			filepath.Join(env.home, "Library", "LaunchAgents", launchAgentFile))
	}
	if _, err := io.WriteString(out, report.String()); err != nil {
		return fmt.Errorf("print what init did: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d steps were skipped and changed nothing; see the output above", failed, len(steps))
	}
	return nil
}

// ensureConfig loads the configuration init will work from, and writes one only
// when there is no file at all. An existing file is read and left byte for byte
// as it is: it is hand-written YAML, and marshalling a struct back over it drops
// every comment and every ordering its author chose.
func ensureConfig(path string, env initEnv) (config.Config, bool, initStep) {
	s := initStep{name: "config"}

	_, err := os.Stat(path)
	switch {
	case err == nil:
		cfg, loadErr := config.Load(path)
		if loadErr != nil {
			s.err = fmt.Errorf("%s exists and does not parse, and init will not overwrite it: %w", path, loadErr)
			return config.Config{}, false, s
		}
		s.note = path + " (kept)"
		return cfg, false, s
	case !errors.Is(err, fs.ErrNotExist):
		s.err = fmt.Errorf("read %s: %w", path, err)
		return config.Config{}, false, s
	}

	boardPath, err := chosenBoard(env)
	if err != nil {
		s.err = err
		return config.Config{}, false, s
	}
	cfg := config.Default()
	cfg.BoardPath = boardPath
	if err := config.Save(path, cfg); err != nil {
		s.err = err
		return config.Config{}, false, s
	}
	s.note = fmt.Sprintf("%s (created, board: %s)", path, cfg.BoardPath)
	return cfg, true, s
}

// chosenBoard resolves the board directory for a machine that has no
// configuration yet: the --board flag if it was given, and otherwise a directory
// under the home directory init was handed. Nothing here is allowed to name a
// path belonging to any particular machine.
func chosenBoard(env initEnv) (string, error) {
	if env.board == "" {
		return filepath.Join(env.home, "fleetdeck", "board"), nil
	}
	abs, err := filepath.Abs(env.board)
	if err != nil {
		return "", fmt.Errorf("resolve --board %s: %w", env.board, err)
	}
	return abs, nil
}

// ensureBoard creates the board directory and, when that directory is absent or
// empty, writes one example card into it. A directory that already holds files is
// somebody's board and is not touched.
func ensureBoard(cfgPath string, cfg config.Config, cfgCreated bool, cfgErr error, env initEnv) initStep {
	s := initStep{name: "board"}
	if cfgErr != nil {
		s.err = fmt.Errorf("the configured board is unknown while %s cannot be read", cfgPath)
		return s
	}

	dir := cfg.BoardPath
	if !cfgCreated {
		// An existing config names the board, and init does not edit an existing
		// config — so a flag that disagrees with it has nowhere to be recorded.
		switch {
		case dir == "":
			s.err = fmt.Errorf("%s sets no board.path, and init does not rewrite a configuration file it did not create: set board.path there and re-run", cfgPath)
			return s
		case env.board != "":
			given, err := filepath.Abs(env.board)
			if err != nil {
				s.err = fmt.Errorf("resolve --board %s: %w", env.board, err)
				return s
			}
			if given != dir {
				s.err = fmt.Errorf("--board %s contradicts board.path %s already set in %s; init does not rewrite that file, so change it there or drop the flag", given, dir, cfgPath)
				return s
			}
		}
	}

	empty, existed, err := dirIsEmpty(dir)
	if err != nil {
		s.err = err
		return s
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.err = fmt.Errorf("create board dir: %w", err)
		return s
	}
	if !empty {
		s.note = dir + " (kept, it already holds files)"
		return s
	}
	card := filepath.Join(dir, exampleCardName)
	if err := os.WriteFile(card, []byte(exampleCard(time.Now().Format(dateLayout))), 0o600); err != nil {
		s.err = fmt.Errorf("write example card: %w", err)
		return s
	}
	if existed {
		s.note = fmt.Sprintf("%s (was empty, wrote %s)", dir, exampleCardName)
		return s
	}
	s.note = fmt.Sprintf("%s (created, wrote %s)", dir, exampleCardName)
	return s
}

// dirIsEmpty reports whether dir holds no entries at all, and whether it existed
// in the first place. A path that is not a directory is an error here rather than
// something to create over.
func dirIsEmpty(dir string) (empty, existed bool, err error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, false, nil
	case err != nil:
		return false, false, fmt.Errorf("read board dir %s: %w", dir, err)
	}
	return len(entries) == 0, true, nil
}

// exampleCard is the board template of spec line 277: a card that parses, that
// passes the board's own schema, and that shows a new operator what the fields
// mean. Its field conventions are documented in docs/en/board-convention.md.
func exampleCard(today string) string {
	return `---
zone: unplanned
stage: new
progress: 0
created: ` + today + `
---

# An example card

## Context

This card was written by ` + "`fleetdeck init`" + ` because the board was empty, and an
empty board is indistinguishable from a broken one. Replace it with a real task,
or delete it once the board has cards of its own.

A card is an ordinary markdown file. The frontmatter above is what the panel
reads: ` + "`zone`" + `, ` + "`stage`" + `, ` + "`progress`" + `, ` + "`created`" + `, and — once a session is working the
task — ` + "`session`" + `. The allowed values of each field, and the rules connecting them,
are in docs/en/board-convention.md.

## Log

- ` + today + `: created by fleetdeck init.
`
}

// ensureStatusline points Claude Code's statusline at the reporter, having first
// checked that the reporter is actually there.
func ensureStatusline(env initEnv) initStep {
	s := initStep{name: "statusline"}

	statusBinary := filepath.Join(filepath.Dir(env.binary), statusBinaryName)
	if _, err := os.Stat(statusBinary); err != nil {
		// Writing this path anyway would break the status line of every Claude Code
		// session on the machine, and would do it silently, in a file this command
		// has no other reason to touch.
		s.err = fmt.Errorf("no %s found at %s (looked beside %s); Claude Code would run a command that is not there, so nothing was written", statusBinaryName, statusBinary, env.binary)
		return s
	}

	settingsPath := filepath.Join(env.home, ".claude", "settings.json")
	result, err := wireStatusline(settingsPath, statusBinary, env.force)
	if err != nil {
		s.err = err
		return s
	}
	s.note = fmt.Sprintf("%s -> %s (%s)", settingsPath, statusBinary, result.what)
	if result.reformatted {
		// Said out loud rather than left to be discovered: this command's promise is
		// that it does not disturb what it did not come for, and re-encoding the
		// whole document is a disturbance even when nothing is lost by it.
		s.detail = "that file was reformatted: two-space indent, keys in alphabetical order — JSON carries no comments to lose, but a hand-ordered file does not come back in its own order"
	}
	return s
}

// statuslineResult is what wireStatusline did.
type statuslineResult struct {
	// what is written, updated, kept or replaced.
	what string
	// reformatted reports that an existing file came back re-encoded — same
	// settings, different layout and key order.
	reformatted bool
}

// wireStatusline sets the statusLine command and leaves every other setting
// untouched.
//
// A malformed file stops the step: overwriting someone's settings to make our own
// feature work is not a trade we get to make. Neither is replacing a statusline
// the operator configured themselves — that needs force, and says so.
func wireStatusline(settingsPath, binary string, force bool) (statuslineResult, error) {
	settings := map[string]any{}
	existed := false
	raw, err := os.ReadFile(settingsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return statuslineResult{}, fmt.Errorf("read settings: %w", err)
	default:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return statuslineResult{}, fmt.Errorf("%s is malformed, refusing to overwrite it: %w", settingsPath, err)
		}
		existed = true
	}

	// Measured before the map is touched: re-encoding the settings exactly as they
	// are and comparing that to the file on disk is what separates "this write will
	// reindent and reorder your file" from "your file is already in that shape".
	reformatted := false
	if existed {
		if canonical, cErr := json.MarshalIndent(settings, "", "  "); cErr == nil {
			reformatted = string(raw) != string(append(canonical, '\n'))
		}
	}

	what := "written"
	line := map[string]any{"type": "command", "command": binary}
	switch existing, present := settings["statusLine"]; {
	case !present:
	case isOurStatusline(existing):
		current, _ := existing.(map[string]any)
		if statuslineCommandOf(existing) == binary {
			// The setting already says what this step would say, so the file is not
			// opened for writing at all — layout included, whatever it is. This is
			// what makes a second run change no file.
			return statuslineResult{what: "kept"}, nil
		}
		// Ours to move, and the operator may have set other keys on it.
		if current != nil {
			line = current
			line["command"] = binary
			if _, ok := line["type"]; !ok {
				line["type"] = "command"
			}
		}
		what = "updated"
	case force:
		what = "replaced"
	default:
		return statuslineResult{}, fmt.Errorf("%s already runs %q as its statusline; re-run with --force to replace it with %s", settingsPath, statuslineCommandOf(existing), binary)
	}

	settings["statusLine"] = line
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return statuslineResult{}, fmt.Errorf("encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return statuslineResult{}, fmt.Errorf("create settings dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o600); err != nil {
		return statuslineResult{}, fmt.Errorf("write settings: %w", err)
	}
	return statuslineResult{what: what, reformatted: reformatted}, nil
}

// statuslineCommandOf digs the command out of a statusLine setting, which Claude
// Code writes as an object and which some hand-written settings carry as a plain
// string.
func statuslineCommandOf(existing any) string {
	switch v := existing.(type) {
	case string:
		return v
	case map[string]any:
		command, _ := v["command"].(string)
		return command
	default:
		return ""
	}
}

// isOurStatusline reports whether a configured statusline is the reporter this
// command installs — by the name of the program it runs, so an installation that
// moved is still recognised as ours rather than as a stranger's.
func isOurStatusline(existing any) bool {
	command := statuslineCommandOf(existing)
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return filepath.Base(fields[0]) == statusBinaryName
}

// ensureLaunchAgent installs the launchd agent that starts the panel at login.
func ensureLaunchAgent(env initEnv) initStep {
	s := initStep{name: "launch agent"}
	agentPath := filepath.Join(env.home, "Library", "LaunchAgents", launchAgentFile)
	// The log belongs with the machine's other logs, not among launchd's agent
	// definitions: ~/Library/LaunchAgents is a directory of plists, and launchd
	// reads what is in it.
	logPath := filepath.Join(env.home, "Library", "Logs", "fleetdeck.log")

	what, err := writeLaunchAgent(agentPath, env.binary, logPath, env.force)
	if err != nil {
		s.err = err
		return s
	}
	s.note = fmt.Sprintf("%s (%s, log: %s)", agentPath, what, logPath)
	return s
}

const launchAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + launchAgentLabel + `</string>
  <key>ProgramArguments</key><array><string>%s</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

// writeLaunchAgent writes the panel's launchd agent, creating the directory its
// log goes in. It returns what it did: created, updated or kept.
//
// An agent file that this command did not write is refused without force: the
// operator's launchd agents are theirs.
func writeLaunchAgent(path, binary, logPath string, force bool) (string, error) {
	body, err := launchAgentPlist(binary, logPath)
	if err != nil {
		return "", err
	}

	what := "created"
	existing, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return "", fmt.Errorf("read launch agent %s: %w", path, err)
	case string(existing) == body:
		return "kept", nil
	case !force && !bytes.Contains(existing, []byte(launchAgentLabel)):
		return "", fmt.Errorf("%s holds an agent this command did not write; re-run with --force to replace it", path)
	default:
		what = "updated"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create launch agent dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return "", fmt.Errorf("create log dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("write launch agent %s: %w", path, err)
	}
	return what, nil
}

// launchAgentPlist renders the agent. Every interpolated value is XML-escaped: a
// home directory or a binary path holding &, < or > otherwise produces a document
// launchd rejects, and nothing on the way there would have said so.
func launchAgentPlist(binary, logPath string) (string, error) {
	escapedBinary, err := escapeXML(binary)
	if err != nil {
		return "", err
	}
	escapedLog, err := escapeXML(logPath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(launchAgentTemplate, escapedBinary, escapedLog, escapedLog), nil
}

func escapeXML(s string) (string, error) {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", fmt.Errorf("escape %q for the launch agent: %w", s, err)
	}
	return b.String(), nil
}
