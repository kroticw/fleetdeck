package board

import (
	"sort"
	"sync"
	"time"
)

// Cache is Scan with each card's parse remembered until the file itself moves.
//
// Scan reads and parses every card on the board, and the panel calls it on every
// collect cycle — a couple of seconds apart — over a board whose cards change far
// more rarely than that. Measured against a 39-card board: a full scan costs 2.9 ms
// and 4.3 MB of garbage, while asking the directory whether anything moved costs
// 68 µs and 25 KB. Two thirds of the parse is the regular expressions over a card's
// body, which is why this remembers the parsed Card and not the bytes.
//
// Freshness is judged by size and modification time, the same pair the panel
// already uses for transcripts. A card rewritten to exactly its old length within
// the same timestamp tick is served stale; the board is written by people and by
// the panel's own editor, not by a process racing the clock.
//
// One Cache serves every board the panel reads, keyed by cards directory: a
// collect cycle scans each fleet's board in turn, and what one board holds says
// nothing about another.
//
// A Cache is safe for concurrent use.
type Cache struct {
	mu    sync.Mutex
	cards map[string]map[string]cachedCard // cards directory -> card path -> parse
}

type cachedCard struct {
	card  Card
	size  int64
	mtime time.Time
}

func NewCache() *Cache {
	return &Cache{cards: map[string]map[string]cachedCard{}}
}

// Scan is board.Scan, answering from memory for every card that has not changed
// since the last call. Errors are the plain Scan's own, including ErrNoCardsDir
// for a path that names no board.
//
// Entries for cards that are no longer there are dropped, so a long-running panel
// does not keep one per card the board has ever held.
func (c *Cache) Scan(dir string) ([]Card, error) {
	cardsDir := CardsDir(dir)
	entries, err := readCardEntries(cardsDir)
	if err != nil {
		return nil, scanErr(cardsDir, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	known := c.cards[cardsDir]
	fresh := make(map[string]cachedCard, len(entries))
	cards := make([]Card, 0, len(entries))
	for _, e := range entries {
		hit, ok := known[e.path]
		if !ok || hit.size != e.size || !hit.mtime.Equal(e.mtime) {
			hit = cachedCard{card: parseCardAt(e.path), size: e.size, mtime: e.mtime}
		}
		fresh[e.path] = hit
		cards = append(cards, hit.card)
	}
	c.cards[cardsDir] = fresh

	sort.Slice(cards, func(i, j int) bool { return cards[i].Path < cards[j].Path })
	return cards, nil
}

// size is the number of cards currently remembered, across every board. For tests.
func (c *Cache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, board := range c.cards {
		n += len(board)
	}
	return n
}
