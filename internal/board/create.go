package board

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// maxTitleRunes bounds a title typed into the panel. A card title is one
	// line on a kanban card; anything longer is a body typed into the wrong box.
	maxTitleRunes = 200
	// maxSlugLen bounds the part of the file name made from the title, in bytes.
	maxSlugLen = 60
	// maxNameAttempts bounds the retry when the name a claimed number produces
	// is somehow already on disk. A claimed number is exclusive, so the name it
	// makes is unique by construction and this should never spin — it is here so
	// that a board left in a state nobody predicted fails with a message rather
	// than looping.
	maxNameAttempts = 100
)

// beforeCardInPlace is called once a new card's content is written and before
// the card is in place under its name: a test's look at that moment.
var beforeCardInPlace = func(_ string) {}

// linkCard puts a written card in place under its name; a test swaps it for a
// file system that makes no hard links.
var linkCard = os.Link

// ErrInvalidCard means a card to be created would not be a valid card: an
// unknown zone, or a title that is empty, too long or more than one line.
var ErrInvalidCard = errors.New("invalid card")

// validZones is the board's vocabulary of zones, the one validate_cards.py and
// the panel's board view both use.
var validZones = map[string]bool{"urgent": true, "unplanned": true, "planned": true, "niceToHave": true}

// translit spells a Russian letter in latin letters for a file name. Card
// titles are written in Russian and card files are named in latin letters —
// the operator's own convention.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e", 'ж': "zh",
	'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o",
	'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts",
	'ч': "ch", 'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu",
	'я': "ya",
}

// CreateCard starts a card on the board at boardDir: a title and a zone, in
// stage new at progress 0, created on the given day. Nothing else — the rest of
// a card is written by the agent or the person who takes the task on, and the
// panel only has to be able to start one.
//
// The card gets a number first (see claimNumber): the board's validator
// requires one, and a card the panel made without one is a card the board
// refuses — which is what the button did before the numbering scheme reached
// the template. The number is claimed through the same .ids/T-NNN marker the
// board's own scripts/new_card.py claims, so the panel and the scripts cannot
// hand the same number to two cards.
//
// The file is cards/T-NNN-<date>-<slug>.md, the slug made from the title. It is
// created exclusively, and no existing card is ever overwritten. It returns the
// path of the new card.
func CreateCard(boardDir, title, zone string, day time.Time) (string, error) {
	title = strings.TrimSpace(title)
	switch {
	case !validZones[zone]:
		return "", fmt.Errorf("%w: unknown zone %q", ErrInvalidCard, zone)
	case title == "":
		return "", fmt.Errorf("%w: the title is empty", ErrInvalidCard)
	case utf8.RuneCountInString(title) > maxTitleRunes:
		return "", fmt.Errorf("%w: the title is longer than %d characters", ErrInvalidCard, maxTitleRunes)
	case strings.IndexFunc(title, unicode.IsControl) >= 0:
		return "", fmt.Errorf("%w: the title must be one line", ErrInvalidCard)
	}

	cardsDir := CardsDir(boardDir)
	if info, err := os.Stat(cardsDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNoCardsDir, cardsDir)
	}

	date := day.Format("2006-01-02")
	tail := date + "-" + slug(title)
	start := nextNumber(boardDir)
	for attempt := 0; attempt < maxNameAttempts; attempt++ {
		number, err := claimNumber(boardDir, start)
		if err != nil {
			return "", err
		}
		start = number + 1

		id := fmt.Sprintf(idFormat, number)
		path := filepath.Join(cardsDir, id+"-"+tail+".md")
		content := fmt.Sprintf("---\nid: %s\nzone: %s\nstage: new\nprogress: 0\ncreated: %s\n---\n\n# %s\n", id, zone, date, title)
		placed, err := placeCard(cardsDir, path, content)
		if err != nil {
			return "", err
		}
		if !placed {
			// The number is spent either way: a claimed number is never
			// released, so the next attempt takes the one after it.
			continue
		}
		return path, nil
	}
	return "", fmt.Errorf("create card: %d names starting %s are already taken", maxNameAttempts, tail)
}

// placeCard puts a new card with content at path, whole or not at all, and
// never over a file already there: it reports false, with nothing written,
// when path is taken.
//
// The card is written to a hidden file beside it first and linked into place:
// a link is atomic, and fails, as the exclusive create it replaces did, when
// the name is taken. A panel can be stopped at any moment -- an update stops
// the panel it replaces, with SIGKILL if it does not go at once (T-060) -- and
// a card created in place and cut off half-written would be a broken card on
// the board. The hidden file's name starts with a dot and does not end in .md,
// so neither a scan of the board nor its validator takes it for a card, and it
// is removed whatever happens.
//
// A board on a file system that makes no hard links -- exFAT, an SMB or FUSE
// mount refuse link(2) with ENOTSUP or EPERM -- gets its card as it did before:
// by an exclusive create at its name. Never over a card there either, but not
// whole-or-nothing: that file system gives no way to be.
func placeCard(cardsDir, path, content string) (bool, error) {
	clearLeftovers(cardsDir)
	tmp, err := os.CreateTemp(cardsDir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("create card: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.WriteString(content)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if werr != nil || serr != nil || cerr != nil {
		return false, fmt.Errorf("write card %s: %w", path, errors.Join(werr, serr, cerr))
	}
	beforeCardInPlace(path)
	if err := linkCard(tmp.Name(), path); err != nil {
		switch {
		case errors.Is(err, fs.ErrExist):
			return false, nil
		case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.EPERM):
			return createCardInPlace(cardsDir, path, content)
		}
		return false, fmt.Errorf("create card: %w", err)
	}
	syncDir(cardsDir)
	return true, nil
}

// createCardInPlace is placeCard where hard links are refused: an exclusive
// create at path, a card cut off by an error removed again.
func createCardInPlace(cardsDir, path, content string) (bool, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create card: %w", err)
	}
	_, werr := f.WriteString(content)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil || serr != nil || cerr != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("write card %s: %w", path, errors.Join(werr, serr, cerr))
	}
	syncDir(cardsDir)
	return true, nil
}

// leftoverAge is how old a card's temp file is before it is taken for one a
// killed write left behind. Writing a card takes milliseconds.
const leftoverAge = time.Minute

// clearLeftovers removes the temp files of card writes a kill cut off (T-060):
// hidden, named for a card, and leftoverAge old. A younger one may belong to a
// write still in progress, and stays.
func clearLeftovers(cardsDir string) {
	entries, err := os.ReadDir(cardsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".") || !strings.Contains(name, ".md.tmp-") || !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) >= leftoverAge {
			_ = os.Remove(filepath.Join(cardsDir, name))
		}
	}
}

// syncDir makes a name just added to dir durable, as far as dir lets it.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// slug makes a file name part from a title: latin letters and digits, Russian
// letters spelled in latin, everything else a single dash, at most maxSlugLen
// bytes. A title with nothing to spell becomes "card".
func slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		var part string
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			part = string(r)
		default:
			spelled, ok := translit[r]
			if !ok {
				dash = b.Len() > 0
				continue
			}
			part = spelled
		}
		if part == "" {
			continue
		}
		if dash {
			b.WriteByte('-')
			dash = false
		}
		b.WriteString(part)
	}
	s := b.String()
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "-")
	}
	if s == "" {
		return "card"
	}
	return s
}
