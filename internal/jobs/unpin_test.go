package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePins(t *testing.T, dir string, ids ...string) string {
	t.Helper()
	path := filepath.Join(dir, pinsFile)
	body, err := json.MarshalIndent(ids, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readPins(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.Unmarshal(body, &ids); err != nil {
		t.Fatalf("the pin set must stay a JSON array the picker can read: %v (%s)", err, body)
	}
	return ids
}

func TestUnpinDropsTheSessionAndKeepsTheRest(t *testing.T) {
	dir := t.TempDir()
	path := writePins(t, dir, "aaa11111", "bbb22222", "ccc33333")

	if err := Unpin(dir, "bbb22222"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	got := readPins(t, path)
	want := []string{"aaa11111", "ccc33333"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("every other pin must survive, got %v", got)
	}
}

// Another fleet's session is pinned and this one never was: nothing to do, and
// nothing to report either.
func TestUnpinIsQuietWhenTheSessionWasNotPinned(t *testing.T) {
	dir := t.TempDir()
	path := writePins(t, dir, "aaa11111")

	if err := Unpin(dir, "bbb22222"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if got := readPins(t, path); len(got) != 1 || got[0] != "aaa11111" {
		t.Fatalf("an unpinned session must leave the set alone, got %v", got)
	}
}

// A machine where nobody has ever pinned anything has no pin file, and an
// acceptance there must not fail over it.
func TestUnpinWithNoPinFileIsNotAFailure(t *testing.T) {
	dir := t.TempDir()
	if err := Unpin(dir, "aaa11111"); err != nil {
		t.Fatalf("a store with no pin set is an empty one: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, pinsFile)); !os.IsNotExist(err) {
		t.Fatalf("nothing was pinned, so nothing may be written: %v", err)
	}
}

func TestUnpinRefusesAnEmptySession(t *testing.T) {
	if err := Unpin(t.TempDir(), ""); err == nil {
		t.Fatal("an unpin naming no session must be refused, not applied to whatever is first")
	}
}

// The picker holds its own lock while it writes; a write that ignored it would
// drop whatever the picker was in the middle of.
func TestUnpinWaitsForNoOneWhenTheLockIsStale(t *testing.T) {
	dir := t.TempDir()
	path := writePins(t, dir, "aaa11111", "bbb22222")
	if err := os.Mkdir(path+".lock", 0o700); err != nil {
		t.Fatal(err)
	}
	old := staleLock * 2
	if err := os.Chtimes(path+".lock", nowMinus(old), nowMinus(old)); err != nil {
		t.Fatal(err)
	}

	if err := Unpin(dir, "aaa11111"); err != nil {
		t.Fatalf("a lock nobody holds any more must be taken over: %v", err)
	}
	if got := readPins(t, path); len(got) != 1 || got[0] != "bbb22222" {
		t.Fatalf("unexpected pin set: %v", got)
	}
}
