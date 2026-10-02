package board

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CardsDir returns the directory Scan and Watch actually read cards from:
// the board directory's "cards" subdirectory. This is the one place that
// convention is spelled out — every caller that needs to know where a
// board's cards live goes through this function rather than joining
// "cards" itself, so the convention cannot drift between Scan, Watch and
// init.
//
// The convention comes from plugin/templates/board/ (cards/, archive/,
// scripts/, README.md), which is what a real, human-maintained board looks
// like. Scan used to read boardDir itself, which happened to work against
// any t.TempDir() a test built with cards sitting flat at the top — and
// silently returned zero cards against a real board shaped like the
// template, where the config's board.path names the directory holding
// cards/, not cards/ itself.
func CardsDir(boardDir string) string {
	return filepath.Join(boardDir, "cards")
}

// Scan reads every card in dir's cards subdirectory (see CardsDir). A board
// directory with no cards subdirectory at all is an error (ErrNoCardsDir): that
// is what a board.path naming the wrong directory looks like. An empty cards
// subdirectory is an empty board and returns no cards and no error — a new
// board starts that way. The cards/ subdirectory, not a card inside it, is
// what tells a board apart from a wrong path; it used to be a card, and that
// made every new board look broken until something wrote an example card in.
//
// There is deliberately no fallback to reading dir itself when cards/ is
// missing: two directory shapes that both work would mean a typo'd or
// half-migrated board silently starts working differently than intended,
// which is worse than one explicit convention with a clear error when it
// is not met.
func Scan(dir string) ([]Card, error) {
	cardsDir := CardsDir(dir)
	cards, err := readCardsIn(cardsDir)
	if err != nil {
		return nil, scanErr(cardsDir, err)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Path < cards[j].Path })
	return cards, nil
}

// scanErr is how a failed read of a cards directory reaches a caller of Scan —
// shared with Cache.Scan so the two cannot disagree about what a board path
// naming no board looks like.
func scanErr(cardsDir string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNoCardsDir, cardsDir)
	}
	return fmt.Errorf("read board cards dir: %w", err)
}

// readCardsIn parses every .md file directly inside dir. The error is the
// directory read's own, unwrapped, so each caller decides what a missing
// directory means: a board that is not one for Scan, a board with no archive
// yet for SessionCards.
func readCardsIn(dir string) ([]Card, error) {
	entries, err := readCardEntries(dir)
	if err != nil {
		return nil, err
	}
	var cards []Card
	for _, e := range entries {
		cards = append(cards, parseCardAt(e.path))
	}
	return cards, nil
}

// cardEntry is one card file as the directory describes it, before it is read:
// the pair Cache judges freshness by.
type cardEntry struct {
	path  string
	size  int64
	mtime time.Time
}

// readCardEntries lists the card files directly inside dir. The error is the
// directory read's own, unwrapped, for the same reason readCardsIn's is.
func readCardEntries(dir string) ([]cardEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]cardEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		fi, err := e.Info()
		if err != nil {
			// The file went away between the listing and the question. It is
			// not a card any more; the next scan will not list it either.
			continue
		}
		out = append(out, cardEntry{path: p, size: fi.Size(), mtime: fi.ModTime()})
	}
	return out, nil
}

// parseCardAt is ParseCard with a failure carried in the card itself, which is
// how an unparsable card reaches the board: as a card with a ParseError, never
// as a missing row.
func parseCardAt(path string) Card {
	c, err := ParseCard(path)
	if err != nil {
		return Card{Path: path, ParseError: err.Error()}
	}
	return c
}
