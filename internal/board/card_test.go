// internal/board/card_test.go
package board

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestScanBoardWithNoCardsSubdirIsTypedError(t *testing.T) {
	// A board directory with no cards/ subdirectory at all — the exact shape
	// of the production bug this test pins: board.path named a directory
	// laid out like plugin/templates/board/ (cards/, archive/, scripts/,
	// README.md), and Scan used to read the board root directly instead of
	// its cards/ subdirectory, so real cards were never found.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# board\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Scan(dir)
	if !errors.Is(err, ErrNoCardsDir) {
		t.Fatalf("a board directory with no cards/ subdirectory must be its own distinct error, not ErrNoCards: %v", err)
	}
}

// A new board starts empty (the operator's decision, 2026-09-11): what tells a
// board apart from a wrong path is its cards/ subdirectory, which the test
// above pins as ErrNoCardsDir, not a card inside it. A file that is not a card
// — a .gitkeep that keeps cards/ in git — leaves the board just as empty.
func TestScanEmptyCardsSubdirIsAnEmptyBoard(t *testing.T) {
	dir := t.TempDir()
	cardsDir := filepath.Join(dir, "cards")
	if err := os.MkdirAll(cardsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cardsDir, ".gitkeep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cards, err := Scan(dir)
	if err != nil {
		t.Fatalf("a cards/ subdirectory with no cards is an empty board, not a broken one: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("an empty board has no cards, got %d", len(cards))
	}
}

func TestScanKeepsGoingPastOneBrokenCard(t *testing.T) {
	dir := t.TempDir()
	cardsDir := filepath.Join(dir, "cards")
	if err := os.MkdirAll(cardsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCard(t, cardsDir, "good.md", sample)
	writeCard(t, cardsDir, "bad.md", "---\nzone: [\n---\n")
	cards, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("both cards must be returned, the broken one included: %d", len(cards))
	}
}

// TestScanReadsCardsSubdirNotBoardRoot pins the fix itself: a .md file
// sitting directly in the board root (not in cards/) must never be picked
// up by Scan — that would be the flat-fallback shape the design explicitly
// rejects (two silently-working conventions are worse than one explicit
// one), and it would mask the exact bug TestScanBoardWithNoCardsSubdirIsTypedError
// guards against.
func TestScanReadsCardsSubdirNotBoardRoot(t *testing.T) {
	dir := t.TempDir()
	writeCard(t, dir, "root-level.md", sample)
	cardsDir := filepath.Join(dir, "cards")
	if err := os.MkdirAll(cardsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCard(t, cardsDir, "in-cards.md", sample)
	cards, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || filepath.Base(cards[0].Path) != "in-cards.md" {
		t.Fatalf("Scan must read only cards/, never the board root: got %+v", cards)
	}
}

func TestParseCardTolerantOfCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(sample, "\n", "\r\n")
	c, err := ParseCard(writeCard(t, t.TempDir(), "crlf.md", crlf))
	if err != nil {
		t.Fatal(err)
	}
	if c.ParseError != "" {
		t.Fatalf("a CRLF card must parse, got ParseError: %q", c.ParseError)
	}
	if c.Zone != "planned" || c.Stage != "review" || c.Progress != 80 || c.Session != "abc12345" {
		t.Fatalf("fields wrong on a CRLF card: %+v", c)
	}
}

func TestParseCardIgnoresTitleAndLinksInsideFencedCode(t *testing.T) {
	body := "```bash\n# fake title\n[[fake-link]]\n```\n\n# Real title\n\nSee [[real-link]].\n"
	card := "---\nzone: planned\nstage: new\nprogress: 0\nsession: abc12345\nrepo: work/thing\ncreated: 2026-09-09\n---\n\n" + body
	c, err := ParseCard(writeCard(t, t.TempDir(), "fenced.md", card))
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Real title" {
		t.Fatalf("title must come from outside the fence, got %q", c.Title)
	}
	if len(c.Links) != 1 || c.Links[0] != "real-link" {
		t.Fatalf("links must come from outside the fence, got %v", c.Links)
	}
}

func TestParseCardLinkNeedsClosingBrackets(t *testing.T) {
	body := "see [[note without a close\n"
	card := "---\nzone: planned\nstage: new\nprogress: 0\nsession: abc12345\nrepo: work/thing\ncreated: 2026-09-09\n---\n\n" + body
	c, err := ParseCard(writeCard(t, t.TempDir(), "unclosed.md", card))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Links) != 0 {
		t.Fatalf("an unclosed [[ must not swallow the rest of the body as a link, got %v", c.Links)
	}
}

func TestParseCardLinkTrimsHeadingAndAlias(t *testing.T) {
	body := "[[note#heading]] and [[note|alias]]\n"
	card := "---\nzone: planned\nstage: new\nprogress: 0\nsession: abc12345\nrepo: work/thing\ncreated: 2026-09-09\n---\n\n" + body
	c, err := ParseCard(writeCard(t, t.TempDir(), "trim.md", card))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Links) != 2 || c.Links[0] != "note" || c.Links[1] != "note" {
		t.Fatalf("both a heading link and an alias link must resolve to the bare note name, got %v", c.Links)
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
		t.Skip("set FLEETDECK_SMOKE_BOARD_DIR to a real board directory (the one holding cards/, not cards/ itself) to run this")
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
