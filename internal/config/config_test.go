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

// TestLoadPortOutOfRangeIsAnError, and its neighbours below through
// TestLoadNegativeSilenceAfterIsAnError, each pin the specific validate() message their
// fixture is meant to trigger, not just err != nil: asserting only that some error
// came back lets the test go green for the wrong reason — e.g. a fixture that is
// simply malformed YAML would satisfy "err == nil is a failure" without ever reaching
// validate() at all, and a bug that swapped two of validate's checks would still turn
// every one of these fixtures into *some* error.
func TestLoadPortOutOfRangeIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 99999\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a port above 65535 must be rejected")
	}
	if !strings.Contains(err.Error(), "server.port must be between 1 and 65535") {
		t.Fatalf("expected the port-range validation message, got: %v", err)
	}
}

func TestLoadPortZeroIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 0\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a port of 0 must be rejected")
	}
	if !strings.Contains(err.Error(), "server.port must be between 1 and 65535") {
		t.Fatalf("expected the port-range validation message, got: %v", err)
	}
}

func TestLoadPortNegativeIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: -1\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a negative port must be rejected")
	}
	if !strings.Contains(err.Error(), "server.port must be between 1 and 65535") {
		t.Fatalf("expected the port-range validation message, got: %v", err)
	}
}

func TestLoadZeroPollIntervalIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("daemon:\n  poll_interval: 0s\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a zero poll interval must be rejected: it would hot-loop against the daemon socket")
	}
	if !strings.Contains(err.Error(), "daemon.poll_interval must be positive") {
		t.Fatalf("expected the poll-interval validation message, got: %v", err)
	}
}

func TestLoadNegativePollIntervalIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("daemon:\n  poll_interval: -5s\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a negative poll interval must be rejected")
	}
	if !strings.Contains(err.Error(), "daemon.poll_interval must be positive") {
		t.Fatalf("expected the poll-interval validation message, got: %v", err)
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
	_, err := Load(p)
	if err == nil {
		t.Fatal("a negative silence_after must be rejected")
	}
	if !strings.Contains(err.Error(), "notify.silence_after must not be negative") {
		t.Fatalf("expected the silence_after validation message, got: %v", err)
	}
}

// TestLoadBarePollIntervalNumberIsAnError covers the recommendation that a bare number
// for a duration field failed with yaml.v3's raw type-mismatch text ("cannot unmarshal
// !!int 0 into time.Duration") instead of a message a user can act on. `poll_interval:
// 0` (no unit suffix) is the single most likely typo for the intended `poll_interval:
// 0s`, and previously the two produced unrelated-looking errors — this one from the
// YAML decoder, that one from validate() — for what is, from the user's chair, the same
// mistake.
func TestLoadBarePollIntervalNumberIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("daemon:\n  poll_interval: 5\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a bare number for poll_interval must be rejected")
	}
	if strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("expected a comprehensible duration-string error, got yaml.v3's raw type-mismatch text: %v", err)
	}
	if !strings.Contains(err.Error(), "duration string") || !strings.Contains(err.Error(), "bare number") {
		t.Fatalf("expected an error naming both the expected duration-string form and the bare-number mistake, got: %v", err)
	}
}

// TestLoadBareSilenceAfterNumberIsAnError is
// TestLoadBarePollIntervalNumberIsAnError's counterpart for notify.silence_after, the
// config file's other duration field.
func TestLoadBareSilenceAfterNumberIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("notify:\n  silence_after: 30\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a bare number for silence_after must be rejected")
	}
	if strings.Contains(err.Error(), "cannot unmarshal") {
		t.Fatalf("expected a comprehensible duration-string error, got yaml.v3's raw type-mismatch text: %v", err)
	}
	if !strings.Contains(err.Error(), "duration string") || !strings.Contains(err.Error(), "bare number") {
		t.Fatalf("expected an error naming both the expected duration-string form and the bare-number mistake, got: %v", err)
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

// TestMissingAncestorDirsReturnsEveryLevelMkdirAllWouldCreate covers the recommendation
// that Save chmod'ed only the leaf directory MkdirAll created, not any intermediate
// level created in the same call. missingAncestorDirs is the piece that makes fixing
// this possible: it must report every directory in the chain that does not exist yet,
// not just dir itself, so Save can chmod each one explicitly rather than trusting
// MkdirAll's own mode argument — which is subject to umask for every level it creates,
// not only the last — to have gotten every one of them right on its own.
func TestMissingAncestorDirsReturnsEveryLevelMkdirAllWouldCreate(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "a", "b", "c")

	got := missingAncestorDirs(dir)

	want := []string{
		filepath.Join(base, "a"),
		filepath.Join(base, "a", "b"),
		filepath.Join(base, "a", "b", "c"),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d missing directories, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("missing dir %d: expected %q, got %q", i, w, got[i])
		}
	}
}

// TestMissingAncestorDirsStopsAtAnExistingAncestor covers the other half: a directory
// that already exists before Save's call must never appear in the list, since Save
// must never chmod a directory it did not create itself (see
// TestSaveLeavesExistingDirectoryPermissionsUnchanged).
func TestMissingAncestorDirsStopsAtAnExistingAncestor(t *testing.T) {
	base := t.TempDir() // already exists
	dir := filepath.Join(base, "a", "b")

	got := missingAncestorDirs(dir)

	want := []string{
		filepath.Join(base, "a"),
		filepath.Join(base, "a", "b"),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d missing directories, got %d: %v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("missing dir %d: expected %q, got %q", i, w, got[i])
		}
	}
}

// TestMissingAncestorDirsDoesNotTreatEACCESAsMissing covers the recommendation that
// missingAncestorDirs treated any os.Stat error as "does not exist", not just
// fs.ErrNotExist. Here "mid" exists but cannot be stat'ed because its own parent
// ("outer") denies search permission — the same EACCES an existing ancestor with a
// broken symlink parent, or an ENOTDIR from a non-directory earlier in the path, would
// also produce. Absence must mean errors.Is(err, fs.ErrNotExist) alone: on any other
// stat error against an existing ancestor, that ancestor must not join the list and
// must not later get os.Chmod(d, 0o700) from Save — exactly the "silently tighten a
// directory Save has no business touching" the function's own comment promises to
// prevent.
func TestMissingAncestorDirsDoesNotTreatEACCESAsMissing(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permission checks; this test requires a non-root uid")
	}

	base := t.TempDir()
	outer := filepath.Join(base, "outer")
	mid := filepath.Join(outer, "mid")
	dir := filepath.Join(mid, "created1", "created2")

	if err := os.MkdirAll(mid, 0o700); err != nil {
		t.Fatalf("seeding mid: %v", err)
	}
	if err := os.Chmod(outer, 0o000); err != nil {
		t.Fatalf("chmod outer: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(outer, 0o700) }) // let t.TempDir() clean up afterward

	if _, err := os.Stat(mid); err == nil {
		t.Fatal("test setup did not actually reproduce a stat failure on mid; cannot exercise the bug")
	}

	got := missingAncestorDirs(dir)

	for _, d := range got {
		if d == mid {
			t.Fatalf("missingAncestorDirs reported existing (but unstat'able) %q as missing: %v", mid, got)
		}
		if d == outer {
			t.Fatalf("missingAncestorDirs reported existing %q as missing: %v", outer, got)
		}
	}
}

func TestSaveWritesFilePerms0600AndDirPerms0700(t *testing.T) {
	// Use a subdirectory Save must create itself via MkdirAll, so the directory's
	// permissions are also Save's doing, not an artefact of t.TempDir(). Two levels
	// ("sub" and "dir") so the leaf and an intermediate directory MkdirAll created in
	// the same call can be checked separately below.
	base := t.TempDir()
	dir := filepath.Join(base, "sub", "dir")
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

	intermediate := filepath.Join(base, "sub")
	intermediateInfo, err := os.Stat(intermediate)
	if err != nil {
		t.Fatal(err)
	}
	if perm := intermediateInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("expected intermediate dir %q mode 0700, got %o", intermediate, perm)
	}
}

// TestSaveLeavesExistingDirectoryPermissionsUnchanged covers Save applying
// os.Chmod(dir, 0o700) unconditionally, including to a directory it did not create,
// whenever the path came from outside (the plan gives the binary a --config flag).
// `fleetdeck --config ~/fleet.yaml` on a first save would then silently chmod 0700
// $HOME — a destructive, unreversed side effect on a directory that has nothing to do
// with the config. Save must tighten only the directory it created itself (see
// TestSaveWritesFilePerms0600AndDirPerms0700 for that case) and leave an
// already-existing directory's mode exactly as it found it, no matter how loose.
func TestSaveLeavesExistingDirectoryPermissionsUnchanged(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	p := filepath.Join(dir, "c.yaml")

	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o755 {
		t.Errorf("expected Save to leave an existing directory's mode unchanged at 0755, got %o", perm)
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
