package board

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// historyCard is a card as the board's own scripts write one, reduced to the
// fields a session's history reads.
func historyCard(id, session, stage, created, title string) string {
	return "---\nid: " + id + "\nzone: planned\nstage: " + stage + "\nprogress: 0\nsession: " + session +
		"\nrepo: work/thing\ncreated: " + created + "\n---\n\n# " + title + "\n"
}

func newHistoryBoard(t *testing.T, withArchive bool) (dir, cards, archive string) {
	t.Helper()
	dir = t.TempDir()
	cards = filepath.Join(dir, "cards")
	archive = filepath.Join(dir, "archive")
	if err := os.Mkdir(cards, 0o750); err != nil {
		t.Fatal(err)
	}
	if withArchive {
		if err := os.Mkdir(archive, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return dir, cards, archive
}

func TestSessionCardsReadsTheBoardAndTheArchiveInTheOrderTheWorkWasTaken(t *testing.T) {
	dir, cards, archive := newHistoryBoard(t, true)
	// Closed work leaves the board for the archive, and it is still work this
	// session did: a history read from cards/ alone would drop exactly the part
	// of it that is finished.
	writeCard(t, archive, "2026-09-10-old.md", historyCard("T-003", "abc12345", "done", "2026-09-10", "the oldest"))
	writeCard(t, cards, "T-1000-2026-09-12-b.md", historyCard("T-1000", "abc12345", "active", "2026-09-12", "same day, higher number"))
	writeCard(t, cards, "T-999-2026-09-12-a.md", historyCard("T-999", "abc12345", "done", "2026-09-12", "same day, lower number"))
	writeCard(t, cards, "T-050-2026-09-11-other.md", historyCard("T-050", "ffff0000", "active", "2026-09-11", "another session's"))
	// Quoted, the way part of the real board writes it: YAML reads the same value.
	writeCard(t, cards, "T-020-2026-09-11-quoted.md", historyCard("T-020", `"abc12345"`, "review", "2026-09-11", "quoted session"))

	got, err := SessionCards(dir, "abc12345")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.ID)
	}
	// Numbers compared as numbers: as strings T-1000 sorts before T-999.
	want := []string{"T-003", "T-020", "T-999", "T-1000"}
	if len(ids) != len(want) {
		t.Fatalf("cards = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("cards = %v, want %v", ids, want)
		}
	}

	if !got[0].Archived || got[1].Archived {
		t.Fatalf("archived flags wrong: %+v", got)
	}
	if got[0].Title != "the oldest" || got[0].Stage != "done" || got[0].Created != "2026-09-10" {
		t.Fatalf("fields not carried: %+v", got[0])
	}
	if got[3].Path != filepath.Join(cards, "T-1000-2026-09-12-b.md") {
		t.Fatalf("path not carried: %+v", got[3])
	}
}

func TestSessionCardsOfASessionThatTookNothingIsEmptyNotAnError(t *testing.T) {
	dir, cards, _ := newHistoryBoard(t, false)
	writeCard(t, cards, "T-001-2026-09-11-x.md", historyCard("T-001", "ffff0000", "active", "2026-09-11", "not this one"))
	// A card nobody has taken has an empty session, and an empty session must
	// not be read as belonging to a session asked for with an empty id.
	writeCard(t, cards, "T-002-2026-09-11-y.md", historyCard("T-002", `""`, "new", "2026-09-11", "untaken"))

	for _, short := range []string{"abc12345", ""} {
		got, err := SessionCards(dir, short)
		if err != nil {
			t.Fatalf("%q: a board with no archive is an ordinary board: %v", short, err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("%q: want an empty, non-nil list, got %#v", short, got)
		}
	}
}

func TestSessionCardsOfADirectoryThatIsNotABoardIsTheTypedError(t *testing.T) {
	_, err := SessionCards(t.TempDir(), "abc12345")
	if !errors.Is(err, ErrNoCardsDir) {
		t.Fatalf("err = %v, want ErrNoCardsDir", err)
	}
}
