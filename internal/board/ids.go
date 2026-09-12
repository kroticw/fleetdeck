package board

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	// idsDir is the registry of claimed numbers, hidden so that Obsidian keeps
	// it out of the note tree. Its files are empty: the name is the whole
	// content. The board's own scripts/new_card.py uses this exact directory
	// and these exact names — the panel and the scripts are one source of
	// numbers, not two, and that only holds while both claim the same file.
	idsDir = ".ids"
	// idFormat is the number's one written form. Three digits with leading
	// zeros, and beyond that none, so that "T-0001" and "T-001" can never be
	// two spellings of one number.
	idFormat = "T-%03d"
	// archiveDir holds cards that left the board. A card keeps its number
	// there, so the archive counts as taken numbers just as cards/ does.
	archiveDir = "archive"
	// idScanBytes bounds how much of a card is read to find its number: the
	// frontmatter is at the top, and a card whose id is not in the first
	// half-kilobyte has no id.
	idScanBytes = 512
)

// filenameID matches the number at the head of a card's file name, both on a
// finished card ("T-012-2026-09-11-slug.md") and on the stub a claim leaves.
var filenameID = regexp.MustCompile(`^T-(\d{3}|[1-9]\d{3,})(?:-|\.md$)`)

// frontmatterID matches the number in the frontmatter, for a card named before
// numbers existed and not yet renamed.
var frontmatterID = regexp.MustCompile(`(?m)^id:\s*["']?T-(\d{3,})["']?\s*$`)

// cardNumber reads a card's number: from the file name, and failing that from
// the frontmatter. It returns 0 when the card has no number.
func cardNumber(path string) int {
	if m := filenameID.FindStringSubmatch(filepath.Base(path)); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, idScanBytes)
	n, err := f.Read(head)
	if n == 0 && err != nil {
		return 0
	}
	m := frontmatterID.FindSubmatch(head[:n])
	if m == nil {
		return 0
	}
	number, _ := strconv.Atoi(string(m[1]))
	return number
}

// takenNumbers collects every number already spent on this board: the registry
// first, then the cards themselves. The registry is the authority; the cards are
// read for the numbers handed out before the registry existed. The archive is
// read for the same reason it is in the scripts — a card takes its number with
// it, and skipping the archive drops the maximum and hands one number twice.
func takenNumbers(boardDir string) map[int]bool {
	taken := map[int]bool{}

	entries, err := os.ReadDir(filepath.Join(boardDir, idsDir))
	if err == nil {
		for _, entry := range entries {
			digits := strings.TrimPrefix(entry.Name(), "T-")
			if digits == entry.Name() {
				continue
			}
			if n, err := strconv.Atoi(digits); err == nil {
				taken[n] = true
			}
		}
	}

	for _, dir := range []string{CardsDir(boardDir), filepath.Join(boardDir, archiveDir)} {
		cards, err := filepath.Glob(filepath.Join(dir, "*.md"))
		if err != nil {
			continue
		}
		for _, card := range cards {
			if n := cardNumber(card); n != 0 {
				taken[n] = true
			}
		}
	}
	return taken
}

// nextNumber is the first number no card and no marker holds.
func nextNumber(boardDir string) int {
	highest := 0
	for n := range takenNumbers(boardDir) {
		if n > highest {
			highest = n
		}
	}
	return highest + 1
}

// claimNumber takes a number for keeps, starting at start, and returns it.
//
// The claim is the creation of an empty marker file with O_EXCL, which the
// kernel grants exactly once: whoever loses the race gets ErrExist and moves
// on to the next number. There is no counter and no lock file, because cards
// are started in parallel — at the panel, by the orchestrator, by the sessions
// themselves — and "read the maximum, add one" gives one number to two cards.
//
// What is claimed is the number alone, not the card's name: two cards with
// different slugs would claim one number without ever colliding by file name.
func claimNumber(boardDir string, start int) (int, error) {
	registry := filepath.Join(boardDir, idsDir)
	if err := os.MkdirAll(registry, 0o700); err != nil {
		return 0, fmt.Errorf("open the number registry %s: %w", registry, err)
	}
	for number := start; ; number++ {
		marker := filepath.Join(registry, fmt.Sprintf(idFormat, number))
		f, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			// Someone took this number between the scan and the claim.
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("claim number %s: %w", marker, err)
		}
		if err := f.Close(); err != nil {
			return 0, fmt.Errorf("claim number %s: %w", marker, err)
		}
		return number, nil
	}
}
