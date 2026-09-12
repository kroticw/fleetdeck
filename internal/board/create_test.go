package board

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var createDay = time.Date(2026, 9, 11, 18, 30, 0, 0, time.UTC)

func emptyBoard(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(CardsDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCreateCardWritesTheTitleAndZoneAndNothingElse(t *testing.T) {
	dir := emptyBoard(t)
	path, err := CreateCard(dir, "Fix the header clamp", "urgent", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(CardsDir(dir), "T-001-2026-09-11-fix-the-header-clamp.md"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nid: T-001\nzone: urgent\nstage: new\nprogress: 0\ncreated: 2026-09-11\n---\n\n# Fix the header clamp\n"
	if string(raw) != want {
		t.Fatalf("card content:\n%q\nwant\n%q", raw, want)
	}
	c, err := ParseCard(path)
	if err != nil || c.ParseError != "" {
		t.Fatalf("the new card must parse: %v %s", err, c.ParseError)
	}
	if c.Title != "Fix the header clamp" || c.Zone != "urgent" || c.Stage != "new" || c.Progress != 0 || c.Created != "2026-09-11" {
		t.Fatalf("parsed card: %+v", c)
	}
}

// The operator writes titles in Russian and names card files in latin letters.
func TestCreateCardTransliteratesACyrillicTitleIntoTheFileName(t *testing.T) {
	dir := emptyBoard(t)
	path, err := CreateCard(dir, "fleetdeck: рабочая папка, доска и щётки", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != "T-001-2026-09-11-fleetdeck-rabochaya-papka-doska-i-shchetki.md" {
		t.Fatalf("file name = %s", got)
	}
	c, _ := ParseCard(path)
	if c.Title != "fleetdeck: рабочая папка, доска и щётки" {
		t.Fatalf("the title itself stays as written: %q", c.Title)
	}
}

// Two cards of one day may carry the same title; what keeps them apart is the
// number, which no two cards share.
func TestCreateCardNeverOverwritesACardWithTheSameName(t *testing.T) {
	dir := emptyBoard(t)
	first, err := CreateCard(dir, "Same", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, []byte("---\nid: T-001\nzone: planned\n---\n\n# Same, edited by an agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := CreateCard(dir, "Same", "urgent", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second) != "T-002-2026-09-11-same.md" {
		t.Fatalf("a second card of the same name must get its own file, got %s", second)
	}
	raw, _ := os.ReadFile(first)
	if !strings.Contains(string(raw), "edited by an agent") {
		t.Fatal("the first card was overwritten")
	}
}

func TestCreateCardRefusesWhatWouldNotBeAValidCard(t *testing.T) {
	cases := map[string]struct{ title, zone string }{
		"unknown zone":      {"A task", "someday"},
		"empty zone":        {"A task", ""},
		"empty title":       {"", "planned"},
		"blank title":       {"   \t ", "planned"},
		"title with a line": {"A task\n---\nzone: urgent", "planned"},
		"title too long":    {strings.Repeat("x", maxTitleRunes+1), "planned"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := emptyBoard(t)
			_, err := CreateCard(dir, tc.title, tc.zone, createDay)
			if !errors.Is(err, ErrInvalidCard) {
				t.Fatalf("want ErrInvalidCard, got %v", err)
			}
			entries, _ := os.ReadDir(CardsDir(dir))
			if len(entries) != 0 {
				t.Fatalf("a refused card left a file behind: %v", entries)
			}
		})
	}
}

func TestCreateCardTrimsTheTitle(t *testing.T) {
	dir := emptyBoard(t)
	path, err := CreateCard(dir, "  Spaced out  ", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	// Read as bytes: ParseCard trims a title itself, so asking it would not
	// show whether the file carries the spaces.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n# Spaced out\n") {
		t.Fatalf("the title must be written trimmed:\n%q", raw)
	}
}

// Spaces alone are no title, whether or not a control character is among them.
func TestCreateCardRefusesATitleOfSpacesAlone(t *testing.T) {
	dir := emptyBoard(t)
	if _, err := CreateCard(dir, "     ", "planned", createDay); !errors.Is(err, ErrInvalidCard) {
		t.Fatalf("want ErrInvalidCard, got %v", err)
	}
}

func TestCreateCardNamesATitleWithNoLettersCard(t *testing.T) {
	dir := emptyBoard(t)
	path, err := CreateCard(dir, "!!! ???", "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "T-001-2026-09-11-card.md" {
		t.Fatalf("file name = %s", filepath.Base(path))
	}
}

func TestCreateCardKeepsTheFileNameShort(t *testing.T) {
	dir := emptyBoard(t)
	path, err := CreateCard(dir, strings.Repeat("word ", 40), "planned", createDay)
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "T-001-2026-09-11-"), ".md")
	if len(slug) > maxSlugLen || strings.HasSuffix(slug, "-") || slug == "" {
		t.Fatalf("slug %q must be non-empty, at most %d bytes and not end in a dash", slug, maxSlugLen)
	}
}

// A board path with no cards directory is the wrong path (ErrNoCardsDir), and a
// card must not be what makes it look like a board.
func TestCreateCardRefusesABoardWithNoCardsDirectory(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateCard(dir, "A task", "planned", createDay); !errors.Is(err, ErrNoCardsDir) {
		t.Fatalf("want ErrNoCardsDir, got %v", err)
	}
	if _, err := os.Stat(CardsDir(dir)); !os.IsNotExist(err) {
		t.Fatal("CreateCard made a cards directory on a path that was not a board")
	}
}

// The card the panel makes passes the board's own validator — the one agents
// run — not only this package's parser.
func TestCreatedCardPassesTheBoardValidator(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the validator cannot run here")
	}
	validator, err := filepath.Abs(filepath.Join("..", "..", "plugin", "templates", "board", "scripts", "validate_cards.py"))
	if err != nil {
		t.Fatal(err)
	}
	dir := emptyBoard(t)
	for _, zone := range []string{"urgent", "unplanned", "planned", "niceToHave"} {
		path, err := CreateCard(dir, "Card in "+zone, zone, createDay)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(python, validator, path).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "все карточки валидны") {
			t.Fatalf("the validator rejects a card the panel made (%s): %v\n%s", zone, err, out)
		}
	}
}
