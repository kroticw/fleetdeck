package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file must not be an error, got %v", err)
	}
	if got.ServerPort != Default().ServerPort {
		t.Fatalf("missing file must yield defaults, got %+v", got)
	}
}

func TestLoadEmptyFileReturnsDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.yaml")
	if err := os.WriteFile(p, []byte(""), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("an empty file must not be an error, got %v", err)
	}
	if got.ServerPort != Default().ServerPort || got.DaemonPollInterval != Default().DaemonPollInterval {
		t.Fatalf("an empty file must yield defaults, got %+v", got)
	}
}

func TestLoadCommentOnlyFileReturnsDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "comments.yaml")
	content := "# fleetdeck config\n# nothing set yet\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("a comment-only file must not be an error, got %v", err)
	}
	if got.ServerPort != Default().ServerPort || got.DaemonPollInterval != Default().DaemonPollInterval {
		t.Fatalf("a comment-only file must yield defaults, got %+v", got)
	}
}

func TestLoadBrokenFileIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "broken.yaml")
	// This must be malformed for exactly one reason: invalid YAML syntax (an unclosed
	// flow sequence), under a key ("server.port") that Load actually recognises. The
	// previous fixture, "server_port: [1,2", was invalid YAML AND an unknown top-level
	// key at once, so it was green whether or not the syntax-error path worked at all.
	if err := os.WriteFile(p, []byte("server:\n  port: [1,2\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a broken config must fail loudly, not fall back to defaults")
	}
}

func TestLoadMultipleDocumentsIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "multi.yaml")
	content := "server:\n  port: 1234\n---\nboard:\n  path: /x\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a second YAML document after '---' must be rejected, not silently ignored")
	}
}

func TestLoadOverridesOnlyGivenKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 9001\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.ServerPort != 9001 {
		t.Fatalf("port not applied: %d", got.ServerPort)
	}
	if got.DaemonPollInterval != Default().DaemonPollInterval {
		t.Fatalf("untouched key must keep its default, got %v", got.DaemonPollInterval)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	want := Default()
	want.BoardPath = "/tmp/board"
	want.Notify.SilenceAfter = 45 * time.Minute
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.BoardPath != want.BoardPath || got.Notify.SilenceAfter != want.Notify.SilenceAfter {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestLoadNestedConfigOverridesExactlyNamedKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	content := "board:\n  path: /custom/board\nserver:\n  port: 8080\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.BoardPath != "/custom/board" {
		t.Fatalf("board.path not applied: %q", got.BoardPath)
	}
	if got.ServerPort != 8080 {
		t.Fatalf("server.port not applied: %d", got.ServerPort)
	}
	if got.DaemonPollInterval != Default().DaemonPollInterval {
		t.Fatalf("daemon.poll_interval should be default, got %v", got.DaemonPollInterval)
	}
	if got.Notify.Waiting != Default().Notify.Waiting {
		t.Fatalf("notify.enabled.waiting should be default")
	}
}

func TestLoadUsageEnabledFalse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("usage:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsageEnabled != false {
		t.Fatalf("usage.enabled: false must yield false, got %v", got.UsageEnabled)
	}
}

func TestLoadUsageEnabledDefaultWhenMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsageEnabled != Default().UsageEnabled {
		t.Fatalf("usage.enabled should be default when missing, got %v", got.UsageEnabled)
	}
}

func TestLoadPartialNotifyOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	content := "notify:\n  enabled:\n    waiting: false\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Notify.Waiting != false {
		t.Fatalf("notify.enabled.waiting should be false, got %v", got.Notify.Waiting)
	}
	if got.Notify.Failed != Default().Notify.Failed {
		t.Fatalf("notify.enabled.failed should be default, got %v", got.Notify.Failed)
	}
	if got.Notify.Silent != Default().Notify.Silent {
		t.Fatalf("notify.enabled.silent should be default, got %v", got.Notify.Silent)
	}
	if got.Notify.CardBlocked != Default().Notify.CardBlocked {
		t.Fatalf("notify.enabled.card_blocked should be default, got %v", got.Notify.CardBlocked)
	}
	if got.Notify.SilenceAfter != Default().Notify.SilenceAfter {
		t.Fatalf("notify.silence_after should be default, got %v", got.Notify.SilenceAfter)
	}
}

func TestLoadUnknownKeyIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("unknown_key: value\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("unknown key must be an error")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error should mention unknown key, got: %v", err)
	}
}

func TestLoadFlatLegacyKeysAreRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("board_path: /x\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("flat legacy key board_path must be rejected")
	}
}

func TestSavePreservesNestedFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	want := Default()
	want.BoardPath = "/my/board"
	want.ServerPort = 9999
	want.Notify.Waiting = false
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	if !strings.Contains(content, "board:") || !strings.Contains(content, "path:") {
		t.Fatalf("saved config should have nested board.path, got:\n%s", content)
	}
	if !strings.Contains(content, "server:") || !strings.Contains(content, "port:") {
		t.Fatalf("saved config should have nested server.port, got:\n%s", content)
	}
	if !strings.Contains(content, "enabled:") {
		t.Fatalf("saved config should have nested notify.enabled, got:\n%s", content)
	}
}

func TestLoadPortOutOfRangeIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 99999\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a port above 65535 must be rejected")
	}
}

func TestLoadPortZeroIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 0\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a port of 0 must be rejected")
	}
}

func TestLoadPortNegativeIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: -1\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a negative port must be rejected")
	}
}

func TestLoadZeroPollIntervalIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("daemon:\n  poll_interval: 0s\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a zero poll interval must be rejected: it would hot-loop against the daemon socket")
	}
}

func TestLoadNegativePollIntervalIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("daemon:\n  poll_interval: -5s\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a negative poll interval must be rejected")
	}
}

// TestLoadNegativeSilenceAfterIsAnError covers the must-fix item that validate caught a
// non-positive poll_interval but let a negative notify.silence_after through both Load
// and Save. A negative silence window means either "always silent" or "never silent"
// depending on how it is later compared, and the user meant neither. Modelled on
// TestLoadNegativePollIntervalIsAnError.
func TestLoadNegativeSilenceAfterIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("notify:\n  silence_after: -5m\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("a negative silence_after must be rejected")
	}
}

func TestSaveRejectsInvalidConfigAndLeavesExistingFileUntouched(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	original := Default()
	original.BoardPath = "/keep/me"
	if err := Save(p, original); err != nil {
		t.Fatalf("seeding a valid config: %v", err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := Save(p, Config{}); err == nil {
		t.Fatal("Save must reject a config that Load would refuse (port 0, poll_interval 0)")
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a rejected Save must not modify the existing file: before=%q after=%q", before, after)
	}
}

func TestSaveWritesCompleteValidYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	want := Default()
	want.BoardPath = "/my/board"
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		t.Fatalf("Save must write complete, valid YAML, got a parse error: %v", err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("reading back a saved config: %v", err)
	}
	if got.BoardPath != want.BoardPath {
		t.Fatalf("round trip lost data: %+v", got)
	}
}

func TestSaveWritesFilePerms0600AndDirPerms0700(t *testing.T) {
	// Use a subdirectory Save must create itself via MkdirAll, so the directory's
	// permissions are also Save's doing, not an artefact of t.TempDir().
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	p := filepath.Join(dir, "c.yaml")

	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}

	fileInfo, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected config file mode 0600, got %o", perm)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("expected config dir mode 0700, got %o", perm)
	}
}

func TestLoadValidPortAndIntervalAreAccepted(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	content := "server:\n  port: 65535\ndaemon:\n  poll_interval: 1s\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("valid boundary values must be accepted, got %v", err)
	}
	if got.ServerPort != 65535 {
		t.Fatalf("expected port 65535, got %d", got.ServerPort)
	}
	if got.DaemonPollInterval != time.Second {
		t.Fatalf("expected poll interval 1s, got %v", got.DaemonPollInterval)
	}
}
