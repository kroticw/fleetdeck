package board

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
	// maxSameName bounds how many cards of one day may share a title.
	maxSameName = 100
)

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
// The file is cards/<date>-<slug>.md, the slug made from the title. It is
// created exclusively: a card that already has that name gets a -2, -3, ...
// file instead, and no existing card is ever overwritten. It returns the path
// of the new card.
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
	content := fmt.Sprintf("---\nzone: %s\nstage: new\nprogress: 0\ncreated: %s\n---\n\n# %s\n", zone, date, title)
	base := date + "-" + slug(title)
	for n := 1; n <= maxSameName; n++ {
		name := base + ".md"
		if n > 1 {
			name = fmt.Sprintf("%s-%d.md", base, n)
		}
		path := filepath.Join(cardsDir, name)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create card: %w", err)
		}
		_, werr := f.WriteString(content)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			// A half-written card is a broken card on the board; the file was
			// ours alone a moment ago, so it goes.
			_ = os.Remove(path)
			return "", fmt.Errorf("write card %s: %w", path, errors.Join(werr, cerr))
		}
		return path, nil
	}
	return "", fmt.Errorf("create card: %d cards named %s already exist", maxSameName, base)
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
