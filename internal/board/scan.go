package board

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scan reads every card in dir. An empty directory is an error, not an empty success.
func Scan(dir string) ([]Card, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read board dir: %w", err)
	}
	var cards []Card
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		c, err := ParseCard(p)
		if err != nil {
			c = Card{Path: p, ParseError: err.Error()}
		}
		cards = append(cards, c)
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("%w in %s", ErrNoCards, dir)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Path < cards[j].Path })
	return cards, nil
}
