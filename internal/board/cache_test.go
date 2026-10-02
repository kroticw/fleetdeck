// internal/board/cache_test.go
package board

import (
	"os"
	"strings"
	"testing"
)

// writeCardIn writes the sample card into a board's cards directory, creating
// it, and returns the file's path.
func writeCardIn(t *testing.T, boardDir, name string) string {
	t.Helper()
	dir := CardsDir(boardDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return writeCard(t, dir, name, sample)
}

func TestCacheScanMatchesScan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCardIn(t, dir, "a.md")
	writeCardIn(t, dir, "b.md")

	want, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewCache().Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("cached scan returned %d cards, plain scan %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Path != want[i].Path || got[i].ID != want[i].ID || got[i].Body != want[i].Body {
			t.Fatalf("card %d differs:\n cached %+v\n plain  %+v", i, got[i], want[i])
		}
	}
}

// A card whose size and modification time have not moved is not read again.
// Rewriting its body behind the cache's back — same length, same timestamp —
// is what makes a served-from-memory answer observable.
func TestCacheScanReusesUnchangedCard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeCardIn(t, dir, "a.md")

	c := NewCache()
	first, err := c.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	altered := []byte(sample)
	copy(altered[len(altered)-10:], []byte("XXXXXXXXX\n"))
	if err := os.WriteFile(path, altered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}

	got, err := c.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one card, got %d", len(got))
	}
	if got[0].Body != first[0].Body {
		t.Fatal("an unchanged card must be served from the cache, not read again")
	}
}

func TestCacheScanSeesChangedCard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeCardIn(t, dir, "a.md")

	c := NewCache()
	if _, err := c.Scan(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(sample+"\nещё строка, и файл стал длиннее\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := c.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one card, got %d", len(got))
	}
	if !strings.Contains(got[0].Body, "ещё строка") {
		t.Fatalf("a changed card must be read again, body is %q", got[0].Body)
	}
}

func TestCacheScanForgetsRemovedCard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeCardIn(t, dir, "a.md")
	writeCardIn(t, dir, "b.md")

	c := NewCache()
	if _, err := c.Scan(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	got, err := c.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one card after the other was removed, got %d", len(got))
	}
	if n := c.size(); n != 1 {
		t.Fatalf("the removed card must leave no entry behind, cache holds %d", n)
	}
}

// One Cache serves every fleet's board, so scanning one board must not drop
// what is remembered about another.
func TestCacheScanKeepsOtherBoards(t *testing.T) {
	t.Parallel()
	first, second := t.TempDir(), t.TempDir()
	writeCardIn(t, first, "a.md")
	writeCardIn(t, second, "b.md")

	c := NewCache()
	if _, err := c.Scan(first); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Scan(second); err != nil {
		t.Fatal(err)
	}
	if n := c.size(); n != 2 {
		t.Fatalf("both boards must stay remembered, cache holds %d cards", n)
	}
}

// A board whose cards directory is missing is the same error it always was:
// the cache must not turn a wrong board path into an empty board.
func TestCacheScanReportsMissingCardsDir(t *testing.T) {
	t.Parallel()
	if _, err := NewCache().Scan(t.TempDir()); err == nil {
		t.Fatal("a board with no cards directory must be an error")
	}
}
