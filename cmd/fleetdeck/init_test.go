package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

func TestLaunchAgentContainsBinaryAndKeepAlive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.plist")
	if _, err := writeLaunchAgent(p, "/usr/local/bin/fleetdeck", filepath.Join(dir, "logs", "fleetdeck.log"), false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	for _, want := range []string{"/usr/local/bin/fleetdeck", "KeepAlive", "RunAtLoad"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("launch agent missing %q", want)
		}
	}
}

func TestLaunchAgentEscapesXMLSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.plist")
	binary := filepath.Join(dir, "tools & <utils>", "fleetdeck")
	logPath := filepath.Join(dir, "logs & more", "fleetdeck.log")

	if _, err := writeLaunchAgent(p, binary, logPath, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	// Parsed, not grepped: a path with & in it produces a document launchd
	// rejects long before anyone reads the file, and only a parser sees that.
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = true
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("launch agent is not well-formed XML: %v\n%s", err, raw)
		}
		if chars, ok := token.(xml.CharData); ok {
			text.Write(chars)
		}
	}
	for _, want := range []string{binary, logPath} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("parsed plist does not carry %q", want)
		}
	}
}

func TestLaunchAgentCreatesItsLogDirectory(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "Library", "Logs", "fleetdeck.log")
	if _, err := writeLaunchAgent(filepath.Join(dir, "agent.plist"), "/bin/fleetdeck", logPath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(logPath)); err != nil {
		t.Fatalf("log directory was not created: %v", err)
	}
}

func TestLaunchAgentRefusesToOverwriteAForeignAgent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.plist")
	if err := os.WriteFile(p, []byte("<plist>someone else's agent</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLaunchAgent(p, "/bin/fleetdeck", filepath.Join(dir, "fleetdeck.log"), false); err == nil {
		t.Fatal("an agent file that is not ours must not be overwritten without --force")
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "someone else") {
		t.Fatal("refused step rewrote the file anyway")
	}
	if _, err := writeLaunchAgent(p, "/bin/fleetdeck", filepath.Join(dir, "fleetdeck.log"), true); err != nil {
		t.Fatalf("--force must replace it: %v", err)
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
	if !strings.Contains(out.String(), cfg.BoardPath) {
		t.Fatalf("init must print the board it chose, got:\n%s", out.String())
	}
	if got := statuslineCommand(t, settingsPathOf(home)); !strings.HasSuffix(got, statusBinaryName) {
		t.Fatalf("statusline command = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", launchAgentFile)); err != nil {
		t.Fatalf("launch agent was not written: %v", err)
	}
	if !strings.Contains(out.String(), "launchctl") {
		t.Fatalf("init must print the launchctl command it deliberately does not run:\n%s", out.String())
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

func TestInitWritesAnExampleCardThatParses(t *testing.T) {
	home := t.TempDir()
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: io.Discard}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "fleetdeck", "board", exampleCardName)

	card, err := board.ParseCard(path)
	if err != nil {
		t.Fatalf("the example card cannot be read: %v", err)
	}
	if card.ParseError != "" {
		t.Fatalf("the example card does not parse: %s", card.ParseError)
	}
	if card.Zone == "" || card.Stage == "" || card.Created == "" || card.Title == "" {
		t.Fatalf("the example card is missing fields the board expects: %+v", card)
	}

	cards, err := board.Scan(filepath.Dir(path))
	if err != nil {
		t.Fatalf("a board holding only the example card must scan: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("expected one card, got %d", len(cards))
	}
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
	if _, err := os.Stat(filepath.Join(boardDir, exampleCardName)); !os.IsNotExist(err) {
		t.Fatal("an example card must not be added to a board that already holds cards")
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
	if _, statErr := os.Stat(settingsPathOf(home)); !os.IsNotExist(statErr) {
		t.Fatal("Claude Code's settings must not be pointed at a command that is not there")
	}
	// Every other step still ran.
	if _, statErr := os.Stat(filepath.Join(home, "fleetdeck", "board", exampleCardName)); statErr != nil {
		t.Fatalf("the board step was skipped along with the statusline: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "Library", "LaunchAgents", launchAgentFile)); statErr != nil {
		t.Fatalf("the launch agent step was skipped along with the statusline: %v", statErr)
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
