// internal/board/card_test.go
package board

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const sample = `---
zone: planned
stage: review
progress: 80
session: abc12345
repo: work/thing
created: 2026-09-09
---

# BS-1 — заголовок

Тело со связью [[other-card]] и ещё одной [[third]].
`

func writeCard(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseCardReadsFieldsTitleAndLinks(t *testing.T) {
	c, err := ParseCard(writeCard(t, t.TempDir(), "c.md", sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Zone != "planned" || c.Stage != "review" || c.Progress != 80 || c.Session != "abc12345" {
		t.Fatalf("fields wrong: %+v", c)
	}
	if c.Title != "BS-1 — заголовок" {
		t.Fatalf("title wrong: %q", c.Title)
	}
	if len(c.Links) != 2 || c.Links[0] != "other-card" || c.Links[1] != "third" {
		t.Fatalf("links wrong: %v", c.Links)
	}
}

func TestParseCardWithBrokenFrontmatterReportsInsteadOfFailing(t *testing.T) {
	c, err := ParseCard(writeCard(t, t.TempDir(), "b.md", "---\nzone: [unclosed\n---\n\nbody\n"))
	if err != nil {
		t.Fatalf("a broken card must be reported through ParseError, not through err: %v", err)
	}
	if c.ParseError == "" {
		t.Fatal("ParseError must say what went wrong")
	}
}

func TestParseCardWithoutFrontmatterIsBroken(t *testing.T) {
	c, _ := ParseCard(writeCard(t, t.TempDir(), "n.md", "# no frontmatter\n"))
	if c.ParseError == "" {
		t.Fatal("a file without frontmatter is not a card and must say so")
	}
}

func TestScanEmptyDirectoryIsTypedError(t *testing.T) {
	_, err := Scan(t.TempDir())
	if !errors.Is(err, ErrNoCards) {
		t.Fatalf("an empty board must be reported, not pass as success with zero cards: %v", err)
	}
}

func TestScanKeepsGoingPastOneBrokenCard(t *testing.T) {
	dir := t.TempDir()
	writeCard(t, dir, "good.md", sample)
	writeCard(t, dir, "bad.md", "---\nzone: [\n---\n")
	cards, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("both cards must be returned, the broken one included: %d", len(cards))
	}
}

// TestScanRealBoardDirectory runs Scan against a real, human-maintained board rather
// than a directory this test built for itself. Cards written by agents carry shapes a
// hand-made fixture does not: Cyrillic titles, empty field values, wiki links with
// aliases. A suite that only ever reads its own t.TempDir() output proves the parser
// agrees with the test author, not that it agrees with the board.
func TestScanRealBoardDirectory(t *testing.T) {
	dir := os.Getenv("FLEETDECK_SMOKE_BOARD_DIR")
	if dir == "" {
		t.Skip("set FLEETDECK_SMOKE_BOARD_DIR to a real board cards directory to run this")
	}
	cards, err := Scan(dir)
	if err != nil {
		t.Fatalf("scan real board %s: %v", dir, err)
	}
	parsed := 0
	for _, c := range cards {
		if c.ParseError == "" {
			parsed++
		}
	}
	if parsed == 0 {
		t.Fatalf("scanned %d cards in %s and parsed none of them: looking and finding "+
			"nothing is not the same as there being nothing to look at", len(cards), dir)
	}
	t.Logf("scanned %d cards, %d parsed", len(cards), parsed)
}
