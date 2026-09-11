package board

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	entries, err := os.ReadDir(cardsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoCardsDir, cardsDir)
		}
		return nil, fmt.Errorf("read board cards dir: %w", err)
	}
	var cards []Card
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(cardsDir, e.Name())
		c, err := ParseCard(p)
		if err != nil {
			c = Card{Path: p, ParseError: err.Error()}
		}
		cards = append(cards, c)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Path < cards[j].Path })
	return cards, nil
}
