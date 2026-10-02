package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const repoCard = "---\nid: T-001\nzone: planned\nstage: new\nprogress: 0\nsession: \"\"\ncreated: 2026-09-29\n---\n\n# Задача\n"

func writeRepoCard(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "T-001-card.md")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The field is optional, so a card may have no line for it; the write adds one
// after session, where scripts/new_card.py puts it.
func TestSetFieldAddsARepoToACardWithout(t *testing.T) {
	p := writeRepoCard(t, repoCard)
	if err := SetField(p, "repo", "src/fleetdeck", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "session: \"\"\nrepo: src/fleetdeck\ncreated:") {
		t.Fatalf("repo was not written after session:\n%s", raw)
	}
	c, err := ParseCard(p)
	if err != nil || c.Repo != "src/fleetdeck" {
		t.Fatalf("read back repo %q, %v", c.Repo, err)
	}
}

func TestSetFieldChangesARepoAndReadsTheTildeAsHome(t *testing.T) {
	p := writeRepoCard(t, strings.Replace(repoCard, "created:", "repo: src/old\ncreated:", 1))
	old := "src/old"
	if err := SetField(p, "repo", "~/src/new", &old); err != nil {
		t.Fatal(err)
	}
	if c, _ := ParseCard(p); c.Repo != "src/new" {
		t.Fatalf("repo = %q, want src/new", c.Repo)
	}
}

// The field names a directory under home and nothing else; what the
// dispatcher would refuse later is refused at the write.
func TestSetFieldRefusesARepoOutsideHome(t *testing.T) {
	for _, v := range []string{"", "/etc", "../x", "src/../../x", "a\nb"} {
		p := writeRepoCard(t, repoCard)
		if err := SetField(p, "repo", v, nil); err == nil {
			t.Errorf("repo %q was written", v)
		}
	}
}

func TestCreateCardWritesTheRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(CardsDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := CreateCard(dir, NewCard{Title: "Задача", Zone: "planned", Repo: "~/src/fleetdeck"}, time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := ParseCard(p); c.Repo != "src/fleetdeck" {
		t.Fatalf("repo = %q, want src/fleetdeck", c.Repo)
	}
	if _, err := CreateCard(dir, NewCard{Title: "Задача", Zone: "planned", Repo: "/etc"}, time.Now()); err == nil {
		t.Fatal("a card was created with a repo outside home")
	}
}

// No repo is still a card: the field is optional on the board, and it is the
// dispatch that asks for it.
func TestCreateCardWithoutARepoWritesNoLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(CardsDir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := CreateCard(dir, NewCard{Title: "Задача", Zone: "planned"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(p); strings.Contains(string(raw), "repo:") {
		t.Fatalf("a card created without a repo has a repo line:\n%s", raw)
	}
}
