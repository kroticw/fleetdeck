package board

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// archiveFile is the board's registry of sessions that have been put out. It
// is a markdown table, written by a hand until now, and the board's own
// template ships it with nothing but a heading.
const archiveFile = "AGENTS-ARCHIVE.md"

// ArchiveEntry is one session recorded before it is put out: when, which
// session, what it was called, the card it worked, and what it leaves behind.
type ArchiveEntry struct {
	When    time.Time
	Session string
	Name    string
	// Card is the card's own path, absolute as the panel holds it. It is
	// recorded relative to the board.
	Card string
	// Words are the last thing the session said, and Transcript is where the
	// rest of what it said stays after it is gone.
	Words      string
	Transcript string
}

// AppendArchive records e at the end of the board's archive and answers with
// the file it wrote.
//
// The registry is started when the board has none: a session put out with
// nowhere to record it loses everything it knew, and an archive missing from an
// old board is no reason for that.
func AppendArchive(boardDir string, e ArchiveEntry) (string, error) {
	if strings.TrimSpace(e.Session) == "" {
		return "", errors.New("an archive entry must name the session it is about")
	}
	dir := filepath.Join(boardDir, archiveDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("make the board's archive directory: %w", err)
	}
	path := filepath.Join(dir, archiveFile)

	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var out strings.Builder
	if len(body) == 0 {
		out.WriteString(archiveHeading)
	} else {
		out.Write(body)
		if body[len(body)-1] != '\n' {
			out.WriteByte('\n')
		}
	}
	out.WriteString(e.row(boardDir))
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// archiveHeading is what a registry started here begins with: the table's own
// header, without which the first row joins nothing and every reader after it
// has to guess the columns. The board template's wording, so a board that gets
// its archive here and one rolled out with it read the same.
const archiveHeading = "# Registry of torn-down sessions\n\n" +
	"| Date | Short id | Session name | Card | Result |\n" +
	"| --- | --- | --- | --- | --- |\n"

func (e ArchiveEntry) row(boardDir string) string {
	card := e.Card
	if rel, err := filepath.Rel(boardDir, e.Card); err == nil && !strings.HasPrefix(rel, "..") {
		card = rel
	}
	result := e.Words
	if e.Transcript != "" {
		result = strings.TrimSpace(result + " " + fmt.Sprintf("Transcript: `%s`", e.Transcript))
	}
	return fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
		e.When.Format(time.DateOnly), cell(e.Session), cell(e.Name), "`"+cell(card)+"`", cell(result))
}

// cell is text made safe to sit in one table cell: a pipe would end the cell
// and take the rest of the sentence into the next column, and a newline would
// end the row and leave the tail outside the table, where nothing reads it.
func cell(text string) string {
	text = strings.ReplaceAll(text, "|", `\|`)
	return strings.Join(strings.Fields(text), " ")
}
