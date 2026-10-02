package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var when = time.Date(2026, 9, 24, 15, 4, 0, 0, time.UTC)

func archiveWith(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, archiveDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, archiveDir, archiveFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const archiveHead = "# Registry\n\n| Date | Short id | Session name | Card | Result |\n| --- | --- | --- | --- | --- |\n"

func TestAppendArchiveAddsARowUnderTheOnesAlreadyThere(t *testing.T) {
	dir := archiveWith(t, archiveHead+"| 2026-09-01 | aaa11111 | first | `cards/T-001.md` | nothing |\n")

	path, err := AppendArchive(dir, ArchiveEntry{
		When:    when,
		Session: "bbb22222",
		Name:    "T-052 stage by hand",
		Card:    filepath.Join(dir, "cards", "T-052.md"),
		Words:   "the branch is pushed",
	})
	if err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	last := lines[len(lines)-1]
	for _, want := range []string{"2026-09-24", "bbb22222", "T-052 stage by hand", "`cards/T-052.md`", "the branch is pushed"} {
		if !strings.Contains(last, want) {
			t.Fatalf("the new row must carry %q: %s", want, last)
		}
	}
	if !strings.Contains(string(body), "aaa11111") {
		t.Fatalf("the rows already recorded must survive:\n%s", body)
	}
}

// The card is named the way every other row names it: relative to the board, so
// the entry still points somewhere after the board is moved.
func TestAppendArchiveNamesTheCardRelativeToTheBoard(t *testing.T) {
	dir := archiveWith(t, archiveHead)

	path, err := AppendArchive(dir, ArchiveEntry{
		When: when, Session: "bbb22222", Card: filepath.Join(dir, "cards", "T-052.md"), Words: "done",
	})
	if err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), dir) {
		t.Fatalf("no absolute path belongs in the archive:\n%s", body)
	}
}

// A cell holding a pipe or a newline ends the row early and the rest of the
// session's words land outside the table, where nothing reads them.
func TestAppendArchiveKeepsTheSessionsWordsInsideOneCell(t *testing.T) {
	dir := archiveWith(t, archiveHead)

	path, err := AppendArchive(dir, ArchiveEntry{
		When: when, Session: "bbb22222", Card: filepath.Join(dir, "cards", "T-052.md"),
		Words: "ran `a | b`\nand then\n\nstopped",
	})
	if err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	body, _ := os.ReadFile(path)
	rows := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	last := rows[len(rows)-1]
	if strings.Count(last, "|")-strings.Count(last, `\|`) != 6 {
		t.Fatalf("the row must have exactly the table's columns: %s", last)
	}
	if !strings.Contains(last, "and then") || !strings.Contains(last, "stopped") {
		t.Fatalf("nothing the session said may be dropped: %s", last)
	}
}

// A board rolled out before the archive existed, or one whose archive was
// removed, still has to record the session: the entry is the only thing that
// outlives it.
func TestAppendArchiveStartsTheRegistryWhenThereIsNone(t *testing.T) {
	dir := archiveWith(t, "")

	path, err := AppendArchive(dir, ArchiveEntry{
		When: when, Session: "bbb22222", Card: filepath.Join(dir, "cards", "T-052.md"), Words: "done",
	})
	if err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "| --- |") {
		t.Fatalf("a registry started here must be a table, or the next row has nothing to join:\n%s", body)
	}
	if !strings.Contains(string(body), "bbb22222") {
		t.Fatalf("the entry must be in it:\n%s", body)
	}
}

// A file that does not end in a newline would otherwise take the new row onto
// the end of the last one.
func TestAppendArchiveStartsANewLineWhenTheFileDoesNotEndOne(t *testing.T) {
	dir := archiveWith(t, archiveHead+"| 2026-09-01 | aaa11111 | first | `cards/T-001.md` | nothing |")

	path, err := AppendArchive(dir, ArchiveEntry{
		When: when, Session: "bbb22222", Card: filepath.Join(dir, "cards", "T-052.md"), Words: "done",
	})
	if err != nil {
		t.Fatalf("AppendArchive: %v", err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "nothing || 2026-09-24") {
		t.Fatalf("the rows ran together:\n%s", body)
	}
}

func TestAppendArchiveRefusesAnEntryWithNothingToRecord(t *testing.T) {
	dir := archiveWith(t, archiveHead)
	if _, err := AppendArchive(dir, ArchiveEntry{When: when, Card: "c.md", Words: "done"}); err == nil {
		t.Fatal("an entry naming no session records nothing and must be refused")
	}
}
