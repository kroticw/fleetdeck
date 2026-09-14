package board

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A panel killed while it writes a card -- an update kills a staged panel that
// does not go at once (T-060) -- leaves the card's hidden temp file behind. The
// next card clears what such a write left; a temp file a write in progress may
// still own, one less than a minute old, stays.
func TestCreatingACardClearsWhatAKilledWriteLeftBehind(t *testing.T) {
	dir := emptyBoard(t)
	cards := CardsDir(dir)
	stale := filepath.Join(cards, ".T-001-2026-09-11-cut-off.md.tmp-1234")
	fresh := filepath.Join(cards, ".T-002-2026-09-11-in-work.md.tmp-5678")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("---\nid: T-0"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateCard(dir, "Next", "planned", createDay); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Fatal("the temp file a killed write left two minutes ago is still in the cards directory")
	}
	if _, err := os.Lstat(fresh); err != nil {
		t.Fatalf("a temp file a write in progress may still own was removed: %v", err)
	}
}

// Clearing is by name as well as by age: removing a card is not undone, and
// every card on a board is older than a minute. A card, and files named like
// one or like a temp file without being one, stay however old they are.
func TestCreatingACardClearsNothingButWhatAKilledWriteLeftBehind(t *testing.T) {
	dir := emptyBoard(t)
	cards := CardsDir(dir)
	stale := filepath.Join(cards, ".T-003-2026-09-11-cut-off.md.tmp-1234")
	kept := []string{
		filepath.Join(cards, "T-001-2026-09-10-an-old-card.md"),
		filepath.Join(cards, ".notes.md"),
		filepath.Join(cards, "T-002-2026-09-10-edited.md.bak"),
		filepath.Join(cards, "T-004-2026-09-10-draft.tmp-1"),
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range append([]string{stale}, kept...) {
		if err := os.WriteFile(p, []byte("---\nid: T-001\nzone: planned\n---\n\n# Old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := CreateCard(dir, "Next", "planned", createDay); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stale); err == nil {
		t.Fatal("the temp file a killed write left is still in the cards directory")
	}
	for _, p := range kept {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("creating a card removed %s, which no card write left behind: %v", filepath.Base(p), err)
		}
	}
}
