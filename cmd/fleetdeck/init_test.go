package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
)

// fakeInstall builds a directory holding a fleetdeck binary and, unless withStatus
// is false, the fleetdeck-status reporter beside it. init only ever stats the
// reporter, so an ordinary file is enough to stand in for a real binary.
func fakeInstall(t *testing.T, withStatus bool) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "fleetdeck")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withStatus {
		if err := os.WriteFile(filepath.Join(dir, statusBinaryName), []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return binary
}

// treeHashes maps every file under root to the hash of its contents, so two runs
// of init can be compared file by file instead of by whether they returned nil.
func treeHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	return got
}

func statuslineCommand(t *testing.T, path string) string {
	t.Helper()
	line, ok := readSettings(t, path)["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("settings %s carry no statusLine object", path)
	}
	command, _ := line["command"].(string)
	return command
}

func settingsPathOf(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

func TestWireStatuslineKeepsOtherSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"model":"opus","permissions":{"allow":["Read"]}}`), 0o600)

	if _, err := wireStatusline(p, "/usr/local/bin/fleetdeck-status", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	var got map[string]any
	json.Unmarshal(raw, &got)
	if got["model"] != "opus" {
		t.Fatal("existing settings must survive: the panel edits one key, not the file")
	}
	if !strings.Contains(string(raw), statusBinaryName) {
		t.Fatal("statusline command was not written")
	}
}

func TestWireStatuslineIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{}`), 0o600)
	wireStatusline(p, "/bin/fleetdeck-status", false)
	first, _ := os.ReadFile(p)
	wireStatusline(p, "/bin/fleetdeck-status", false)
	second, _ := os.ReadFile(p)
	if string(first) != string(second) {
		t.Fatal("running init twice must not change anything the second time")
	}
}

func TestWireStatuslineRefusesBrokenSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"model":`), 0o600)
	if _, err := wireStatusline(p, "/bin/fleetdeck-status", false); err == nil {
		t.Fatal("a malformed settings file must stop init, not be overwritten")
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != `{"model":` {
		t.Fatalf("a refused write must leave the file untouched, got %q", raw)
	}
}

func TestWireStatuslineRefusesAForeignCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"statusLine":{"type":"command","command":"/opt/prompt/render"}}`), 0o600)

	_, err := wireStatusline(p, "/bin/fleetdeck-status", false)
	if err == nil {
		t.Fatal("a statusline the operator configured must not be replaced without --force")
	}
	for _, want := range []string{"/opt/prompt/render", "/bin/fleetdeck-status", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %q, got %q", want, err)
		}
	}
	if got := statuslineCommand(t, p); got != "/opt/prompt/render" {
		t.Fatalf("refused step changed the file: statusline is now %q", got)
	}
}

func TestWireStatuslineForceReplacesAForeignCommand(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"model":"opus","statusLine":{"type":"command","command":"/opt/prompt/render"}}`), 0o600)

	if _, err := wireStatusline(p, "/bin/fleetdeck-status", true); err != nil {
		t.Fatal(err)
	}
	if got := statuslineCommand(t, p); got != "/bin/fleetdeck-status" {
		t.Fatalf("--force must replace the statusline, got %q", got)
	}
	if readSettings(t, p)["model"] != "opus" {
		t.Fatal("--force replaces one key, not the file")
	}
}

func TestWireStatuslineUpdatesOurOwnCommandInPlace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"statusLine":{"type":"command","command":"/old/fleetdeck-status","padding":1}}`), 0o600)

	if _, err := wireStatusline(p, "/new/fleetdeck-status", false); err != nil {
		t.Fatalf("our own statusline is ours to move: %v", err)
	}
	line := readSettings(t, p)["statusLine"].(map[string]any)
	if line["command"] != "/new/fleetdeck-status" {
		t.Fatalf("command not updated, got %q", line["command"])
	}
	if line["padding"] != float64(1) {
		t.Fatal("keys the operator set on our own statusline must survive")
	}
}

func TestInitOnAFreshHomeCreatesBoardAndConfigNamingIt(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err != nil {
		t.Fatalf("init on a fresh home must succeed: %v\n%s", err, out.String())
	}

	cfgPath := filepath.Join(home, ".config", "fleetdeck", "config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("init wrote a config it cannot read back: %v", err)
	}
	want := filepath.Join(home, "fleetdeck", "board")
	if cfg.BoardPath != want {
		t.Fatalf("config board path = %q, want %q", cfg.BoardPath, want)
	}
	if _, err := os.Stat(cfg.BoardPath); err != nil {
		t.Fatalf("board directory was not created: %v", err)
	}
	wantDocs := []string{filepath.Join(home, "fleetdeck", "docs")}
	if !slices.Equal(cfg.DocsPaths, wantDocs) {
		t.Fatalf("config docs paths = %q, want %q", cfg.DocsPaths, wantDocs)
	}
	if _, err := os.Stat(wantDocs[0]); err != nil {
		t.Fatalf("docs directory was not created: %v", err)
	}
	if !strings.Contains(out.String(), cfg.BoardPath) {
		t.Fatalf("init must print the board it chose, got:\n%s", out.String())
	}
	if got := statuslineCommand(t, settingsPathOf(home)); !strings.HasSuffix(got, statusBinaryName) {
		t.Fatalf("statusline command = %q", got)
	}
}

func TestInitHonoursTheBoardFlagOnAFreshHome(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(t.TempDir(), "elsewhere", "board")
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), board: want, out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(home, ".config", "fleetdeck", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BoardPath != want {
		t.Fatalf("config board path = %q, want the --board value %q", cfg.BoardPath, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("board directory was not created: %v", err)
	}
}

func TestInitKeepsAnExistingConfigWithItsComments(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, ".config", "fleetdeck", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "# my own notes about this file\nboard:\n  path: " + filepath.Join(home, "cards") + "\nserver:\n  port: 7788\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err != nil {
		t.Fatalf("init over an existing config must succeed: %v\n%s", err, out.String())
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Fatalf("init rewrote the operator's config:\nbefore:\n%s\nafter:\n%s", original, raw)
	}
	if !strings.Contains(string(raw), "# my own notes") {
		t.Fatal("the comment did not survive init")
	}
	if !strings.Contains(out.String(), "kept") {
		t.Fatalf("init must say it kept the config, got:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, "cards")); err != nil {
		t.Fatalf("the configured board must be the one created: %v", err)
	}
}

func TestInitRefusesABoardFlagThatContradictsTheConfig(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, ".config", "fleetdeck", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(home, "cards")
	if err := os.WriteFile(cfgPath, []byte("board:\n  path: "+configured+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runInit(initEnv{home: home, binary: fakeInstall(t, true), board: filepath.Join(home, "other"), out: &out})
	if err == nil {
		t.Fatal("--board must not silently lose to the configured board")
	}
	if !strings.Contains(out.String(), configured) {
		t.Fatalf("the refusal must name the configured board:\n%s", out.String())
	}
	if _, statErr := os.Stat(filepath.Join(home, "other")); statErr == nil {
		t.Fatal("a refused board step must not create the directory anyway")
	}
	// The steps that were not refused still ran.
	if _, statErr := os.Stat(settingsPathOf(home)); statErr != nil {
		t.Fatalf("one refused step aborted the rest: %v", statErr)
	}
}

// A new board starts empty but is the operator's board in every other respect:
// the template's validator, README and archive, and no example card (the
// operator's decision, 2026-09-11).
func TestInitMakesAnEmptyBoardFromTheTemplate(t *testing.T) {
	home := t.TempDir()
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	boardDir := filepath.Join(home, "fleetdeck", "board")

	cards, err := board.Scan(boardDir)
	if err != nil {
		t.Fatalf("a new board must scan as a board: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("a new board is empty, got %d cards", len(cards))
	}
	for _, name := range []string{"README.md", "scripts/validate_cards.py", "archive/AGENTS-ARCHIVE.md"} {
		if _, err := os.Stat(filepath.Join(boardDir, name)); err != nil {
			t.Errorf("the board lacks the template's %s: %v", name, err)
		}
	}
}

// --workspace names the directory the board and the docs are made in, and the
// configuration it creates names both.
func TestInitHonoursTheWorkspaceFlagOnAFreshHome(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "work", "fleet")
	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), workspace: root, out: &out}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	cfg, err := config.Load(filepath.Join(home, ".config", "fleetdeck", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BoardPath != filepath.Join(root, "board") {
		t.Fatalf("config board path = %q, want it under the workspace %q", cfg.BoardPath, root)
	}
	if !slices.Equal(cfg.DocsPaths, []string{filepath.Join(root, "docs")}) {
		t.Fatalf("config docs paths = %q, want the workspace's docs", cfg.DocsPaths)
	}
	if _, err := board.Scan(cfg.BoardPath); err != nil {
		t.Fatalf("the workspace board must scan: %v", err)
	}
	if !strings.Contains(out.String(), root) {
		t.Fatalf("init must print the workspace it made:\n%s", out.String())
	}
}

// A configuration naming a board that could not be made would be a panel
// pointed at nothing — and init, which does not rewrite a configuration it
// finds, would then refuse the corrected path on the next run. So the board
// comes first, and no configuration is written without one.
func TestInitWritesNoConfigurationWhenTheBoardCannotBeMade(t *testing.T) {
	home := t.TempDir()
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runInit(initEnv{home: home, binary: fakeInstall(t, true), workspace: filepath.Join(blocker, "ws"), out: &out})
	if err == nil {
		t.Fatalf("a workspace under a file cannot be made:\n%s", out.String())
	}
	cfgPath := filepath.Join(home, ".config", "fleetdeck", "config.yaml")
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		t.Fatalf("a configuration was written for a board that does not exist:\n%s", out.String())
	}

	// The corrected path then goes through as on a fresh machine.
	good := filepath.Join(t.TempDir(), "ws")
	out.Reset()
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), workspace: good, out: &out}); err != nil {
		t.Fatalf("the corrected path must be accepted: %v\n%s", err, out.String())
	}
	cfg, err := config.Load(cfgPath)
	if err != nil || cfg.BoardPath != filepath.Join(good, "board") {
		t.Fatalf("config board path = %q (%v), want the corrected workspace's board", cfg.BoardPath, err)
	}
}

func TestInitRefusesWorkspaceAndBoardTogether(t *testing.T) {
	home := t.TempDir()
	err := runInit(initEnv{
		home: home, binary: fakeInstall(t, true),
		workspace: filepath.Join(home, "ws"), board: filepath.Join(home, "b"),
		out: io.Discard,
	})
	if err == nil {
		t.Fatal("--workspace and --board name the board twice; one of them would silently lose")
	}
	for _, p := range []string{filepath.Join(home, "ws"), filepath.Join(home, "b"), filepath.Join(home, ".config")} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Fatalf("a refused init created %s", p)
		}
	}
}

// The operator's own case: a configuration naming a board that holds cards.
// init keeps both exactly as they are, and a --workspace that would put the
// board elsewhere is refused, not obeyed half-way.
func TestInitRefusesAWorkspaceFlagThatContradictsTheConfig(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, ".config", "fleetdeck", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(home, "obsidian", "board")
	if err := os.WriteFile(cfgPath, []byte("board:\n  path: "+configured+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runInit(initEnv{home: home, binary: fakeInstall(t, true), workspace: filepath.Join(home, "fleetdeck"), out: &out})
	if err == nil {
		t.Fatal("--workspace must not silently lose to the configured board")
	}
	if !strings.Contains(out.String(), configured) {
		t.Fatalf("the refusal must name the configured board:\n%s", out.String())
	}
	if _, statErr := os.Stat(filepath.Join(home, "fleetdeck")); !os.IsNotExist(statErr) {
		t.Fatal("a refused workspace step must not create the directory anyway")
	}
}

// Agents keep their cards in the board, outside their own working directory,
// so Claude Code must be told they may write there.
func TestInitAllowsAgentsIntoTheWorkspace(t *testing.T) {
	home := t.TempDir()
	settings := settingsPathOf(home)
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"permissions":{"allow":["Read"],"additionalDirectories":["/srv/other"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	perms, _ := readSettings(t, settings)["permissions"].(map[string]any)
	dirs := anyStrings(perms["additionalDirectories"])
	want := []string{"/srv/other", filepath.Join(home, "fleetdeck")}
	if !slices.Equal(dirs, want) {
		t.Fatalf("additionalDirectories = %q, want %q", dirs, want)
	}
	if got := anyStrings(perms["allow"]); !slices.Equal(got, []string{"Read"}) {
		t.Fatalf("other permissions must survive: allow = %q", got)
	}
	if !strings.Contains(out.String(), "permissions:") {
		t.Fatalf("init must report the permissions step:\n%s", out.String())
	}
}

// A directory already allowed, or inside one that is, needs nothing: the file
// is not opened for writing at all.
func TestInitLeavesAnAlreadyAllowedDirectoryAlone(t *testing.T) {
	for name, allowed := range map[string]string{
		"the workspace itself": "fleetdeck",
		"a parent of it":       ".",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			settings := settingsPathOf(home)
			if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
				t.Fatal(err)
			}
			statusBinary := fakeInstall(t, true)
			entry := filepath.Join(home, allowed)
			original := `{"permissions":{"additionalDirectories":["` + entry + `"]},"statusLine":{"command":"` +
				filepath.Join(filepath.Dir(statusBinary), statusBinaryName) + `","type":"command"}}`
			if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := runInit(initEnv{home: home, binary: statusBinary, out: io.Discard}); err != nil {
				t.Fatal(err)
			}
			if raw, _ := os.ReadFile(settings); string(raw) != original {
				t.Fatalf("an allowed directory was written again:\n%s", raw)
			}
		})
	}
}

// With an existing configuration there is no workspace, only the board it
// names — and that board is what agents must be let into.
func TestInitAllowsAgentsIntoTheConfiguredBoard(t *testing.T) {
	home := t.TempDir()
	cfgPath := filepath.Join(home, ".config", "fleetdeck", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(home, "cards-board")
	if err := os.WriteFile(cfgPath, []byte("board:\n  path: "+configured+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	perms, _ := readSettings(t, settingsPathOf(home))["permissions"].(map[string]any)
	if got := anyStrings(perms["additionalDirectories"]); !slices.Equal(got, []string{configured}) {
		t.Fatalf("additionalDirectories = %q, want the configured board", got)
	}
}

// A hand-written entry is often spelled from the home directory.
func TestAllowDirectoryReadsATildeEntryAgainstHome(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(t.TempDir(), "settings.json")
	original := `{"permissions":{"additionalDirectories":["~/fleetdeck"]}}`
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	what, _, err := allowDirectory(p, filepath.Join(home, "fleetdeck", "board"), home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(what, "kept") {
		t.Fatalf("~/fleetdeck already allows the board, got %q", what)
	}
	if raw, _ := os.ReadFile(p); string(raw) != original {
		t.Fatalf("the file was rewritten:\n%s", raw)
	}
}

// A sibling whose name merely starts the same is not inside the allowed one.
func TestAllowDirectoryDoesNotTakeAPrefixForAParent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(`{"permissions":{"additionalDirectories":["/srv/fleet"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	what, _, err := allowDirectory(p, "/srv/fleetdeck", "/home/x")
	if err != nil {
		t.Fatal(err)
	}
	if what != "added" {
		t.Fatalf("/srv/fleet does not hold /srv/fleetdeck, got %q", what)
	}
}

func TestAllowDirectoryRefusesAPermissionsValueOfTheWrongShape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	original := `{"permissions":["Read"]}`
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := allowDirectory(p, "/srv/fleetdeck", "/home/x"); err == nil {
		t.Fatal("a permissions value that is not an object must stop the step, not be replaced")
	}
	if raw, _ := os.ReadFile(p); string(raw) != original {
		t.Fatalf("a refused step changed the file:\n%s", raw)
	}
}

func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
}

func TestInitLeavesABoardThatAlreadyHasFilesAlone(t *testing.T) {
	home := t.TempDir()
	boardDir := filepath.Join(home, "fleetdeck", "board")
	if err := os.MkdirAll(boardDir, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(boardDir, "real-card.md")
	if err := os.WriteFile(existing, []byte("---\nzone: urgent\n---\n\n# Mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(boardDir, "README.md")); !os.IsNotExist(err) {
		t.Fatal("the template must not be spread over a board that already holds files")
	}
	raw, _ := os.ReadFile(existing)
	if !strings.Contains(string(raw), "# Mine") {
		t.Fatal("an existing card was modified")
	}
}

func TestInitRefusesOnlyTheStatuslineStepWhenTheReporterIsMissing(t *testing.T) {
	home := t.TempDir()
	binary := fakeInstall(t, false)

	var out bytes.Buffer
	err := runInit(initEnv{home: home, binary: binary, out: &out})
	if err == nil {
		t.Fatal("a missing fleetdeck-status must make init exit non-zero")
	}
	if !strings.Contains(out.String(), filepath.Join(filepath.Dir(binary), statusBinaryName)) {
		t.Fatalf("the refusal must say where it looked:\n%s", out.String())
	}
	// The file itself exists — the permissions step writes it — but it must
	// carry no statusline at all.
	if _, present := readSettings(t, settingsPathOf(home))["statusLine"]; present {
		t.Fatal("Claude Code's settings must not be pointed at a command that is not there")
	}
	// Every other step still ran.
	if _, statErr := os.Stat(filepath.Join(home, "fleetdeck", "board", "cards")); statErr != nil {
		t.Fatalf("the board step was skipped along with the statusline: %v", statErr)
	}
}

func TestInitRefusesToReplaceAForeignStatusline(t *testing.T) {
	home := t.TempDir()
	settings := settingsPathOf(home)
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"/opt/prompt/render"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err == nil {
		t.Fatal("init must exit non-zero when it refused a step")
	}
	if got := statuslineCommand(t, settings); got != "/opt/prompt/render" {
		t.Fatalf("the operator's statusline was replaced anyway: %q", got)
	}
	if !strings.Contains(out.String(), "--force") {
		t.Fatalf("init must tell the operator how to proceed:\n%s", out.String())
	}

	out.Reset()
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), force: true, out: &out}); err != nil {
		t.Fatalf("--force must complete: %v\n%s", err, out.String())
	}
	if got := statuslineCommand(t, settings); !strings.HasSuffix(got, statusBinaryName) {
		t.Fatalf("--force did not replace the statusline: %q", got)
	}
}

func TestInitTwiceChangesNothing(t *testing.T) {
	home := t.TempDir()
	binary := fakeInstall(t, true)

	if err := runInit(initEnv{home: home, binary: binary, out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	first := treeHashes(t, home)

	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: binary, out: &out}); err != nil {
		t.Fatalf("the second run must succeed too: %v\n%s", err, out.String())
	}
	second := treeHashes(t, home)

	if len(first) != len(second) {
		t.Fatalf("the second run changed the set of files: %d then %d", len(first), len(second))
	}
	for path, sum := range first {
		if second[path] != sum {
			t.Errorf("%s changed on the second run", path)
		}
	}
}

// The fleetdeck window starts the panel now; a launch agent starting a second
// one at login would take the port first and leave the window watching a
// panel it did not start (operator's decision, 2026-09-11).
func TestInitWritesNoLaunchAgent(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err != nil {
		t.Fatalf("init on a fresh home must succeed: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents")); !os.IsNotExist(err) {
		t.Fatalf("init touched ~/Library/LaunchAgents: %v", err)
	}
	if strings.Contains(out.String(), "launchctl") {
		t.Fatalf("init still talks about launchctl on a machine that never had an agent:\n%s", out.String())
	}
}

// An agent an earlier init wrote is the operator's to remove, as loading it
// was: init names it and prints how, in the current spelling, and leaves the
// file exactly as it was.
func TestInitSaysHowToRemoveTheLaunchAgentAnEarlierInitWrote(t *testing.T) {
	home := t.TempDir()
	agent := filepath.Join(home, "Library", "LaunchAgents", launchAgentFile)
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	earlier := []byte("<plist><dict><key>Label</key><string>" + launchAgentLabel + "</string></dict></plist>\n")
	if err := os.WriteFile(agent, earlier, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err != nil {
		t.Fatalf("an old agent is not a failure of init: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		agent,
		"launchctl bootout gui/$(id -u)/" + launchAgentLabel,
		"rm '" + agent + "'",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("init output lacks %q:\n%s", want, out.String())
		}
	}
	// unload is the legacy spelling on current macOS, and this line is the one
	// an operator copies verbatim.
	if strings.Contains(out.String(), "launchctl unload") {
		t.Fatalf("init prints the legacy spelling:\n%s", out.String())
	}
	if raw, _ := os.ReadFile(agent); !bytes.Equal(raw, earlier) {
		t.Fatalf("init changed the agent file:\n%s", raw)
	}
}

// A file at that path that is not an agent init wrote is somebody else's, and
// init has nothing to say about it.
func TestInitSaysNothingAboutAnAgentFileItDidNotWrite(t *testing.T) {
	home := t.TempDir()
	agent := filepath.Join(home, "Library", "LaunchAgents", launchAgentFile)
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent, []byte("<plist>someone else's agent</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "bootout") || strings.Contains(out.String(), agent) {
		t.Fatalf("init offered to remove a file it did not write:\n%s", out.String())
	}
}

func TestInitSaysWhenItReformatsTheSettingsFile(t *testing.T) {
	home := t.TempDir()
	settings := settingsPathOf(home)
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	// Compact, and not in the key order Go's encoder produces.
	if err := os.WriteFile(settings, []byte(`{"model":"opus","alwaysThinkingEnabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: &out}); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "reformatted") {
		t.Fatalf("a write that reindents and reorders the operator's file must say so:\n%s", out.String())
	}
}

func TestInitSaysNothingAboutReformattingWhenItReformatsNothing(t *testing.T) {
	home := t.TempDir()
	binary := fakeInstall(t, true)

	// A settings file that did not exist is created, not reformatted.
	var created bytes.Buffer
	if err := runInit(initEnv{home: home, binary: binary, out: &created}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(created.String(), "reformatted") {
		t.Fatalf("nothing was reformatted on a fresh home:\n%s", created.String())
	}

	// And the second run does not open the file at all.
	var again bytes.Buffer
	if err := runInit(initEnv{home: home, binary: binary, out: &again}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(again.String(), "reformatted") {
		t.Fatalf("a kept file was reported as reformatted:\n%s", again.String())
	}
}

func TestWireStatuslineLeavesAnAlreadyWiredFileUntouchedWhateverItsLayout(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	// Ours already, in a layout this command would never produce.
	original := "{\n\t\"statusLine\": {\"command\": \"/bin/fleetdeck-status\", \"type\": \"command\"},\n\t\"model\": \"opus\"\n}"
	if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := wireStatusline(p, "/bin/fleetdeck-status", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.what != "kept" {
		t.Fatalf("nothing needed changing, got %q", result.what)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != original {
		t.Fatalf("the file was rewritten to satisfy nothing:\n%s", raw)
	}
}
