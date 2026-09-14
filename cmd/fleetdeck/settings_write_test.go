package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ~/.claude/settings.json is Claude Code's own, shared by every session on the
// machine. The panel writes it when it is set up and when a fleet is added, and
// a panel can be stopped at any moment -- an update stops the panel it replaces,
// with SIGKILL if it does not go at once -- so the file is never left
// half-written: the old settings stay whole until the new ones replace them.

func stubSettingsWritten(t *testing.T, f func(string)) {
	t.Helper()
	settingsWritten = f
	t.Cleanup(func() { settingsWritten = func(string) {} })
}

func TestSettingsStayWholeUntilTheNewOnesReplaceThem(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	old := "{\n  \"model\": \"opus\"\n}\n"
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	var during string
	stubSettingsWritten(t, func(string) {
		raw, _ := os.ReadFile(p)
		during = string(raw)
	})

	if err := saveSettings(p, map[string]any{"model": "sonnet"}); err != nil {
		t.Fatal(err)
	}
	if during != old {
		t.Fatalf("while the new settings were being written the file held %q; want the old settings whole", during)
	}
	raw, err := os.ReadFile(p)
	if err != nil || string(raw) != "{\n  \"model\": \"sonnet\"\n}\n" {
		t.Fatalf("the settings hold %q (%v) after saving", raw, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("the settings directory holds %d entries; want the settings alone, nothing left of their writing", len(entries))
	}
}

// A settings file kept in a dotfiles repository is a symlink to it. Replacing
// the symlink with a plain file would quietly cut the settings off from that
// repository.
func TestSavingSettingsThroughASymlinkKeepsTheSymlink(t *testing.T) {
	dir := t.TempDir()
	dotfiles := filepath.Join(dir, "dotfiles")
	if err := os.MkdirAll(dotfiles, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dotfiles, "claude-settings.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := saveSettings(link, map[string]any{"model": "sonnet"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings.json is no longer a symlink after saving (%v, %v)", info, err)
	}
	if got, _ := os.Readlink(link); got != target {
		t.Fatalf("the symlink points at %q, want %q", got, target)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "{\n  \"model\": \"sonnet\"\n}\n" {
		t.Fatalf("the symlink's target holds %q after saving", raw)
	}
}

func TestSavingSettingsKeepsTheFilesPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveSettings(p, map[string]any{"model": "sonnet"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("settings mode after saving: %v (%v), want 0644 as it was", info.Mode().Perm(), err)
	}
}

// Settings that did not exist are created readable by their owner alone, as
// they always were.
func TestSavingSettingsThatDidNotExistMakesThemOwnerOnly(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if err := saveSettings(p, map[string]any{"model": "sonnet"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new settings mode: %v (%v), want 0600", info.Mode().Perm(), err)
	}
}

// A panel killed while it writes the settings leaves their temp file beside
// them. The next save clears what such a write left; a temp file a write in
// progress may still own, one less than a minute old, stays.
func TestSavingSettingsClearsWhatAKilledWriteLeftBehind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	stale := filepath.Join(dir, ".settings.json.tmp-1234")
	fresh := filepath.Join(dir, ".settings.json.tmp-5678")
	for _, f := range []string{stale, fresh} {
		if err := os.WriteFile(f, []byte("{\n  \"mod"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if err := saveSettings(p, map[string]any{"model": "sonnet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Fatal("the temp file a killed write left two minutes ago is still beside the settings")
	}
	if _, err := os.Lstat(fresh); err != nil {
		t.Fatalf("a temp file a write in progress may still own was removed: %v", err)
	}
}

// Clearing is by the settings' own temp name as well as by age: files beside
// the settings that are not their temp files stay however old they are.
func TestSavingSettingsClearsNothingButWhatAKilledWriteLeftBehind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	stale := filepath.Join(dir, ".settings.json.tmp-1234")
	kept := []string{
		filepath.Join(dir, "settings.local.json"),
		filepath.Join(dir, "settings.json.bak"),
		filepath.Join(dir, ".credentials.json"),
		filepath.Join(dir, ".other.json.tmp-1234"),
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, f := range append([]string{stale}, kept...) {
		if err := os.WriteFile(f, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if err := saveSettings(p, map[string]any{"model": "sonnet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Fatal("the temp file a killed write left is still beside the settings")
	}
	for _, f := range kept {
		if _, err := os.Lstat(f); err != nil {
			t.Errorf("saving the settings removed %s, which no settings write left behind: %v", filepath.Base(f), err)
		}
	}
}

// A dotfiles symlink can point at settings that do not exist yet -- a fresh
// clone, a machine being set up -- and, as dotfiles links usually do, by a path
// relative to the link. Saving writes the settings where the link points and
// keeps the link.
func TestSavingSettingsThroughASymlinkToSettingsNotYetWrittenKeepsTheSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "dotfiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(claude, "settings.json")
	relative := filepath.Join("..", "dotfiles", "claude-settings.json")
	if err := os.Symlink(relative, link); err != nil {
		t.Fatal(err)
	}

	if err := saveSettings(link, map[string]any{"model": "sonnet"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("settings.json is no longer a symlink after saving (%v, %v)", info, err)
	}
	if got, _ := os.Readlink(link); got != relative {
		t.Fatalf("the symlink points at %q, want %q", got, relative)
	}
	target := filepath.Join(dir, "dotfiles", "claude-settings.json")
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "{\n  \"model\": \"sonnet\"\n}\n" {
		t.Fatalf("the symlink's target holds %q (%v) after saving", raw, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new settings mode through the symlink: %v (%v), want 0600", info, err)
	}
}
