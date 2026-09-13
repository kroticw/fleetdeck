package board

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SessionCard is one card in a session's history: which card, what became of
// it, and whether it has left the board for the archive. It carries no body —
// a history names the work, the card itself is one click away.
type SessionCard struct {
	Path     string `json:"path"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Stage    string `json:"stage"`
	Created  string `json:"created"`
	Archived bool   `json:"archived"`
}

// SessionCards lists every card whose session field names short, from the
// board's cards and from its archive, in the order the work was taken: by the
// day the card was created, then by its number, then by its path.
//
// The archive is read because that is where finished work goes: a history read
// from cards/ alone loses exactly the part of it that is done. A board with no
// archive yet is an ordinary board. A directory with no cards subdirectory is
// ErrNoCardsDir, as it is for Scan.
//
// A session that took no card has an empty, non-nil history, and so does an
// empty short id: an untaken card's session field is empty too, and matching
// the two would hand every untaken card to a session nobody named.
func SessionCards(boardDir, short string) ([]SessionCard, error) {
	history := []SessionCard{}
	onBoard, err := Scan(boardDir)
	if err != nil {
		return nil, err
	}
	archived, err := readCardsIn(filepath.Join(boardDir, archiveDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if short == "" {
		return history, nil
	}
	for _, group := range []struct {
		cards    []Card
		archived bool
	}{{onBoard, false}, {archived, true}} {
		for _, c := range group.cards {
			if strings.TrimSpace(c.Session) != short {
				continue
			}
			history = append(history, SessionCard{
				Path: c.Path, ID: c.ID, Title: c.Title, Stage: c.Stage, Created: c.Created, Archived: group.archived,
			})
		}
	}
	sort.SliceStable(history, func(i, j int) bool {
		a, b := history[i], history[j]
		if a.Created != b.Created {
			return a.Created < b.Created
		}
		if na, nb := idNumber(a.ID), idNumber(b.ID); na != nb {
			// A card with no number goes after the numbered ones of its day.
			return nb == 0 || (na != 0 && na < nb)
		}
		return a.Path < b.Path
	})
	return history, nil
}

// idNumber is the number in an id written "T-NNN", compared as a number so
// that T-1000 follows T-999. Zero means the card has none.
func idNumber(id string) int {
	digits, found := strings.CutPrefix(id, "T-")
	if !found {
		return 0
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0
	}
	return n
}
