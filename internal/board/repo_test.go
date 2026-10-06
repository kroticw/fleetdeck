package board

import (
	"errors"
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
	for _, v := range []string{"/etc", "../x", "src/../../x", "a\nb", "~user/x", "$HOMEx"} {
		p := writeRepoCard(t, repoCard)
		err := SetField(p, "repo", v, nil)
		var rule *RuleRefusal
		if !errors.As(err, &rule) || rule.Code != codeRepoOutsideHome {
			t.Errorf("repo %q: want a refusal coded %s, got %v", v, codeRepoOutsideHome, err)
		}
	}
}

// The operator's rule (T-134): no repo is the home directory. The ways a
// person spells home are the same value, and it is written as an empty
// string: a bare ~ is YAML's null, and the panel read it back as no repo
// while the operator had just typed one.
func TestNormalizeRepoReadsTheHomeDirectoryAsEmpty(t *testing.T) {
	for _, v := range []string{"", "  ", "~", "~/", " ~ ", "$HOME", "$HOME/", "${HOME}", "${HOME}/"} {
		got, err := NormalizeRepo(v)
		if err != nil || got != "" {
			t.Errorf("NormalizeRepo(%q) = %q, %v; want the home directory, \"\"", v, got, err)
		}
	}
	for v, want := range map[string]string{"$HOME/src/x": "src/x", "${HOME}/src/x": "src/x", "~/src/x/": "src/x"} {
		if got, err := NormalizeRepo(v); err != nil || got != want {
			t.Errorf("NormalizeRepo(%q) = %q, %v; want %q", v, got, err, want)
		}
	}
}

func TestSetFieldWritesTheHomeDirectoryAsAnEmptyRepo(t *testing.T) {
	for _, v := range []string{"~", ""} {
		p := writeRepoCard(t, strings.Replace(repoCard, "created:", "repo: src/old\ncreated:", 1))
		if err := SetField(p, "repo", v, nil); err != nil {
			t.Fatalf("repo %q: %v", v, err)
		}
		raw, _ := os.ReadFile(p)
		if !strings.Contains(string(raw), "\nrepo: \"\"\n") {
			t.Fatalf("repo %q was not written as an empty string:\n%s", v, raw)
		}
		if c, err := ParseCard(p); err != nil || c.Repo != "" {
			t.Fatalf("read back repo %q, %v", c.Repo, err)
		}
	}
}

// RepoDir is where a card's worker starts: the home directory for no repo,
// and a directory that is there for any other, or a refusal that says which.
func TestRepoDirIsAnExistingDirectoryUnderHome(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "src", "proj"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for v, want := range map[string]string{"": home, "~": home, "src/proj": filepath.Join(home, "src", "proj"), "~/src/proj": filepath.Join(home, "src", "proj")} {
		if got, err := RepoDir(home, v); err != nil || got != want {
			t.Errorf("RepoDir(%q) = %q, %v; want %q", v, got, err, want)
		}
	}
	for v, code := range map[string]string{
		"диск забит под завязку, нужно провести анализ, чем": codeRepoNotADirectory,
		"file":    codeRepoNotADirectory,
		"/etc":    codeRepoOutsideHome,
		"../away": codeRepoOutsideHome,
	} {
		_, err := RepoDir(home, v)
		var rule *RuleRefusal
		if !errors.As(err, &rule) || rule.Code != code {
			t.Errorf("RepoDir(%q): want a refusal coded %s, got %v", v, code, err)
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

// No repo is still a card: the field is optional on the board, and a card
// without one is worked in the home directory. "~" is that same card.
func TestCreateCardWithoutARepoWritesNoLine(t *testing.T) {
	for _, repo := range []string{"", "~", "~/"} {
		dir := t.TempDir()
		if err := os.MkdirAll(CardsDir(dir), 0o700); err != nil {
			t.Fatal(err)
		}
		p, err := CreateCard(dir, NewCard{Title: "Задача", Zone: "planned", Repo: repo}, time.Now())
		if err != nil {
			t.Fatalf("repo %q: %v", repo, err)
		}
		if raw, _ := os.ReadFile(p); strings.Contains(string(raw), "repo:") {
			t.Fatalf("a card created with repo %q has a repo line:\n%s", repo, raw)
		}
	}
}
