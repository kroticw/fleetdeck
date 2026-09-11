package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/workspace"
)

const (
	// statusBinaryName is the reporter Claude Code runs for every session. init
	// looks for it beside the fleetdeck binary it was started from.
	statusBinaryName = "fleetdeck-status"

	// launchAgentLabel is the launchd label of the agent earlier versions of this
	// command wrote, and the string that identifies an agent file as theirs. init
	// no longer writes one -- the fleetdeck window starts the panel -- but it
	// still recognises one and says how to remove it.
	launchAgentLabel = "dev.fleetdeck.panel"
	launchAgentFile  = launchAgentLabel + ".plist"

	// defaultWorkspaceName is the directory under the home directory a new
	// workspace goes to when none is named.
	defaultWorkspaceName = "fleetdeck"
)

// initCommand parses the flags of `fleetdeck init` and runs it against this
// machine's own home directory and this binary's own location.
func initCommand(args []string) error {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	workspacePath := flags.String("workspace", "", "directory to make the board and the docs in, recorded in the configuration file this command creates (default ~/fleetdeck)")
	boardPath := flags.String("board", "", "board directory alone, with no workspace around it, to record in the configuration file this command creates")
	force := flags.Bool("force", false, "replace a statusline that init would otherwise refuse to touch")
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
	return runInit(initEnv{home: home, binary: binary, workspace: *workspacePath, board: *boardPath, force: *force, out: os.Stdout})
}

// initEnv is everything runInit is allowed to touch. It is a struct, and every
// path in it is passed in rather than read from the environment inside, so the
// tests can run the whole command against a temporary home instead of against the
// machine the tests are running on.
type initEnv struct {
	// home is the directory holding .config, .claude and Library.
	home string
	// binary is the path of the running fleetdeck binary; the statusline reporter
	// is looked for beside it.
	binary string
	// workspace is the --workspace flag: the directory the board and the docs
	// are made in. Empty means ~/fleetdeck, unless board is given.
	workspace string
	// board is the --board flag: a board directory with no workspace around it.
	board string
	// config is the configuration file to create or keep; empty is
	// ~/.config/fleetdeck/config.yaml under home. The panel's setup passes the
	// file the panel itself reads, which a -config flag may have moved.
	config string
	// force allows the statusline step to replace a statusline the operator
	// configured themselves.
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
	if env.workspace != "" && env.board != "" {
		// Both name the board, and whichever lost would do so silently.
		return errors.New("--workspace and --board both say where the board goes; give one of them")
	}

	steps := initSteps(env)

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

	report.WriteString(earlierLaunchAgent(env.home))
	if _, err := io.WriteString(out, report.String()); err != nil {
		return fmt.Errorf("print what init did: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d steps were skipped and changed nothing; see the output above", failed, len(steps))
	}
	return nil
}

// initSteps performs every step of init and returns what each did. The steps
// are independent — one refused step does not stop the ones after it — except
// that the permissions step lets agents into whatever the board step settled
// on, and has nothing to allow when that step was refused.
func initSteps(env initEnv) []initStep {
	cfgPath := env.config
	if cfgPath == "" {
		cfgPath = filepath.Join(env.home, ".config", "fleetdeck", "config.yaml")
	}
	cfg, cfgNew, cfgStep := ensureConfig(cfgPath, env)
	boardStep, allow := ensureBoard(cfgPath, cfg, cfgNew, cfgStep.err, env)
	if cfgNew && cfgStep.err == nil {
		// Written only now, and only over a board that exists: a configuration
		// naming a board that could not be made points the panel at nothing, and
		// init — which never rewrites a configuration it finds — would then
		// refuse the corrected path on the next run.
		cfgStep = saveNewConfig(cfgPath, cfg, boardStep.err)
	}
	return []initStep{
		cfgStep,
		boardStep,
		ensureStatusline(env),
		ensurePermissions(env, allow),
	}
}

// saveNewConfig writes the configuration ensureConfig planned, unless the board
// it names could not be made.
func saveNewConfig(path string, cfg config.Config, boardErr error) initStep {
	s := initStep{name: "config"}
	if boardErr != nil {
		s.err = fmt.Errorf("%s not written: the board it would name could not be made", path)
		return s
	}
	if err := config.Save(path, cfg); err != nil {
		s.err = err
		return s
	}
	s.note = fmt.Sprintf("%s (created, board: %s)", path, cfg.BoardPath)
	return s
}

// ensureConfig loads the configuration init will work from, or — when there is
// no file at all — plans the one it will write, and says which: isNew. The new
// file is written by saveNewConfig once the board exists. An existing file is
// read and left byte for byte as it is: it is hand-written YAML, and
// marshalling a struct back over it drops every comment and every ordering its
// author chose.
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

	layout, err := chosenLayout(env)
	if err != nil {
		s.err = err
		return config.Config{}, false, s
	}
	cfg := config.Default()
	cfg.BoardPath = layout.board
	if layout.root != "" {
		cfg.DocsPaths = []string{workspace.DocsDir(layout.root)}
	}
	return cfg, true, s
}

// layout is where a machine with no configuration gets its board: a workspace
// root holding the board and the docs, or — with --board — a board alone, and
// then root is empty.
type layout struct {
	root, board string
}

// chosenLayout resolves the layout for a machine that has no configuration yet:
// the --board flag if it was given, and otherwise the --workspace flag or a
// workspace under the home directory init was handed. Nothing here is allowed
// to name a path belonging to any particular machine.
func chosenLayout(env initEnv) (layout, error) {
	if env.board != "" {
		abs, err := filepath.Abs(env.board)
		if err != nil {
			return layout{}, fmt.Errorf("resolve --board %s: %w", env.board, err)
		}
		return layout{board: abs}, nil
	}
	root := filepath.Join(env.home, defaultWorkspaceName)
	if env.workspace != "" {
		abs, err := filepath.Abs(env.workspace)
		if err != nil {
			return layout{}, fmt.Errorf("resolve --workspace %s: %w", env.workspace, err)
		}
		root = abs
	}
	return layout{root: root, board: workspace.BoardDir(root)}, nil
}

// ensureBoard makes the board, and on a machine init just configured, the
// workspace around it: the board template (plugin/templates/board) with an empty
// cards directory, under git, and a docs directory beside it. A board directory
// that already holds files is somebody's board and is not touched; an existing
// configuration's board is made only when its directory is absent or empty, and
// no docs directory is made for it — that configuration names its own docs.
//
// It returns the directory agents must be allowed to write in: the workspace
// root, or the board alone when there is no workspace. Empty when refused.
func ensureBoard(cfgPath string, cfg config.Config, cfgCreated bool, cfgErr error, env initEnv) (initStep, string) {
	s := initStep{name: "board"}
	if cfgErr != nil {
		s.err = fmt.Errorf("the configured board is unknown while %s cannot be read", cfgPath)
		return s, ""
	}

	dir := cfg.BoardPath
	if !cfgCreated {
		if err := flagsAgreeWithConfig(cfgPath, dir, env); err != nil {
			s.err = err
			return s, ""
		}
	}

	if cfgCreated && env.board == "" {
		s.name = "workspace"
		lay, err := chosenLayout(env)
		if err != nil {
			s.err = err
			return s, ""
		}
		res, err := workspace.Create(lay.root, workspace.Options{})
		if err != nil {
			s.err = err
			return s, ""
		}
		s.note = fmt.Sprintf("%s (board %s, docs %s)", res.Root, createdOrKept(res.BoardCreated), createdOrKept(res.DocsCreated))
		s.detail = repoDetail(res.RepoErr)
		return s, res.Root
	}

	created, repoErr, err := workspace.CreateBoard(dir, workspace.Options{})
	if err != nil {
		s.err = err
		return s, ""
	}
	if !created {
		s.note = dir + " (kept, it already holds files)"
		return s, dir
	}
	s.note = dir + " (created from the board template)"
	s.detail = repoDetail(repoErr)
	return s, dir
}

// flagsAgreeWithConfig refuses a --board or --workspace that puts the board
// somewhere other than an existing configuration does: init does not edit a
// configuration it did not create, so the flag has nowhere to be recorded.
func flagsAgreeWithConfig(cfgPath, dir string, env initEnv) error {
	if dir == "" {
		return fmt.Errorf("%s sets no board.path, and init does not rewrite a configuration file it did not create: set board.path there and re-run", cfgPath)
	}
	if env.board == "" && env.workspace == "" {
		return nil
	}
	lay, err := chosenLayout(env)
	if err != nil {
		return err
	}
	if lay.board == dir {
		return nil
	}
	flagName, given := "--board", lay.board
	if env.workspace != "" {
		flagName, given = "--workspace", lay.root
	}
	return fmt.Errorf("%s %s puts the board at %s, which contradicts board.path %s already set in %s; init does not rewrite that file, so change it there or drop the flag", flagName, given, lay.board, dir, cfgPath)
}

func createdOrKept(created bool) string {
	if created {
		return "created"
	}
	return "kept"
}

// repoDetail is the second line of a board step whose board could not be put
// under git — said, because the panel's card writes then cannot be committed.
func repoDetail(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("the board is not under git (%v); the panel will write card fields but cannot commit them", err)
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
	settings, reformatted, err := loadSettings(settingsPath)
	if err != nil {
		return statuslineResult{}, err
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
	if err := saveSettings(settingsPath, settings); err != nil {
		return statuslineResult{}, err
	}
	return statuslineResult{what: what, reformatted: reformatted}, nil
}

// loadSettings reads Claude Code's settings. A missing file is an empty set of
// settings; a malformed one stops the step that asked, since overwriting
// someone's settings to make our own feature work is not a trade we get to
// make.
//
// reformatted is measured before anything is changed: re-encoding the settings
// exactly as they are and comparing that to the file on disk is what separates
// "this write will reindent and reorder your file" from "your file is already in
// that shape".
func loadSettings(settingsPath string) (settings map[string]any, reformatted bool, err error) {
	settings = map[string]any{}
	raw, err := os.ReadFile(settingsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return settings, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("read settings: %w", err)
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, false, fmt.Errorf("%s is malformed, refusing to overwrite it: %w", settingsPath, err)
	}
	if canonical, cErr := json.MarshalIndent(settings, "", "  "); cErr == nil {
		reformatted = string(raw) != string(append(canonical, '\n'))
	}
	return settings, reformatted, nil
}

func saveSettings(settingsPath string, settings map[string]any) error {
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, append(out, '\n'), 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// ensurePermissions lets Claude Code sessions write in dir — the workspace, or
// the board alone — through permissions.additionalDirectories. An agent keeps
// its card on the board, which is outside its own working directory; without
// this entry every card write is a permission prompt nobody is there to answer.
func ensurePermissions(env initEnv, dir string) initStep {
	s := initStep{name: "permissions"}
	if dir == "" {
		s.err = errors.New("the board step was refused, so there is no directory to let agents into")
		return s
	}
	settingsPath := filepath.Join(env.home, ".claude", "settings.json")
	what, reformatted, err := allowDirectory(settingsPath, dir, env.home)
	if err != nil {
		s.err = err
		return s
	}
	s.note = fmt.Sprintf("%s additionalDirectories -> %s (%s)", settingsPath, dir, what)
	if reformatted {
		s.detail = "that file was reformatted: two-space indent, keys in alphabetical order — JSON carries no comments to lose, but a hand-ordered file does not come back in its own order"
	}
	return s
}

// allowDirectory adds dir to permissions.additionalDirectories unless it, or a
// directory holding it, is already there. Then the file is not opened for
// writing at all. An entry may start with "~/", which is read against home.
func allowDirectory(settingsPath, dir, home string) (what string, reformatted bool, err error) {
	settings, reformatted, err := loadSettings(settingsPath)
	if err != nil {
		return "", false, err
	}
	perms := map[string]any{}
	if existing, present := settings["permissions"]; present {
		m, ok := existing.(map[string]any)
		if !ok {
			return "", false, fmt.Errorf("%s has a permissions value that is not an object; refusing to replace it", settingsPath)
		}
		perms = m
	}
	var dirs []any
	if existing, present := perms["additionalDirectories"]; present {
		list, ok := existing.([]any)
		if !ok {
			return "", false, fmt.Errorf("%s has an additionalDirectories value that is not a list; refusing to replace it", settingsPath)
		}
		dirs = list
	}
	for _, entry := range dirs {
		allowed, _ := entry.(string)
		if covers(expandHome(allowed, home), dir) {
			return "kept, already allowed by " + allowed, false, nil
		}
	}
	perms["additionalDirectories"] = append(dirs, dir)
	settings["permissions"] = perms
	if err := saveSettings(settingsPath, settings); err != nil {
		return "", false, err
	}
	return "added", reformatted, nil
}

func expandHome(p, home string) string {
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

// covers reports whether dir is allowed or inside it.
func covers(allowed, dir string) bool {
	if allowed == "" || !filepath.IsAbs(allowed) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(allowed), dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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

// earlierLaunchAgent is what init says about a launch agent an earlier
// version of it wrote: nothing when there is none, or when the file at that
// path is not one init wrote.
//
// The fleetdeck window starts the panel now (the operator's decision,
// 2026-09-11), and an agent left in place starts a second panel at every
// login -- which takes the port first, and leaves the window watching a panel
// it did not start. Removing the agent is the operator's call, as loading it
// was: init prints the two commands and runs neither. bootout, not unload:
// man launchctl lists unload under LEGACY SUBCOMMANDS, and this is the line
// an operator copies verbatim.
func earlierLaunchAgent(home string) string {
	path := filepath.Join(home, "Library", "LaunchAgents", launchAgentFile)
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte(launchAgentLabel)) {
		return ""
	}
	return fmt.Sprintf("\nlaunch agent:  %s is from an earlier fleetdeck init.\n"+
		"               The fleetdeck window starts the panel now, and this agent would start\n"+
		"               a second one at every login. To remove it:\n"+
		"  launchctl bootout gui/$(id -u)/%s\n"+
		"  rm '%s'\n", path, launchAgentLabel, path)
}
