package board

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Some file systems a board can live on make no hard links: exFAT, and SMB and
// FUSE mounts, refuse link(2) with ENOTSUP or EPERM. A card is still created
// there, as it was before cards were linked into place: by an exclusive create
// at its name.

func noHardLinks(t *testing.T, refusal syscall.Errno) {
	t.Helper()
	linkCard = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: refusal}
	}
	t.Cleanup(func() { linkCard = os.Link })
}

func TestACardIsCreatedOnAFileSystemWithoutHardLinks(t *testing.T) {
	for _, refusal := range []syscall.Errno{syscall.ENOTSUP, syscall.EPERM} {
		t.Run(refusal.Error(), func(t *testing.T) {
			noHardLinks(t, refusal)
			dir := emptyBoard(t)

			path, err := CreateCard(dir, "Whole", "planned", createDay)
			if err != nil {
				t.Fatalf("create a card where a hard link is refused with %q: %v", refusal, err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(raw), "# Whole") {
				t.Fatalf("the card at %s holds %q (%v)", path, raw, err)
			}
			entries, err := os.ReadDir(CardsDir(dir))
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Fatalf("the cards directory holds %v; want the card alone, nothing left of its writing", names)
			}
		})
	}
}

func TestACardCreatedWithoutHardLinksNeverOverwritesOne(t *testing.T) {
	noHardLinks(t, syscall.ENOTSUP)
	cards := CardsDir(emptyBoard(t))
	path := filepath.Join(cards, "T-001-2026-09-11-same.md")
	const edited = "---\nid: T-001\nzone: planned\n---\n\n# Same, edited by an agent\n"
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	placed, err := placeCard(cards, path, "---\nid: T-001\nzone: urgent\n---\n\n# Same\n")
	if err != nil || placed {
		t.Fatalf("placing a card at a name already taken reported placed %v (%v); want not placed", placed, err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != edited {
		t.Fatalf("the card already there was overwritten: %q", raw)
	}
}
