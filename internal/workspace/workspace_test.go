package workspace

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/plugin/templates"
)

// noRepo stands in for git in the tests that are not about git, so they run
// the same on a machine with no git at all.
func noRepo(string) error { return nil }

// files lists every regular file under dir, relative to it, skipping .git.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

func TestCreateMakesAnEmptyBoardFromTheTemplateAndADocsDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fleetdeck")

	res, err := Create(root, Options{InitRepo: noRepo})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BoardCreated || !res.DocsCreated {
		t.Fatalf("both parts must report themselves created on a fresh root: %+v", res)
	}
	if res.Board != filepath.Join(root, "board") || res.Docs != filepath.Join(root, "docs") {
		t.Fatalf("unexpected layout: %+v", res)
	}

	want := []string{
		".gitignore",
		"README.md",
		"archive/AGENTS-ARCHIVE.md",
		"cards/.gitkeep",
		"scripts/backfill_ids.py",
		"scripts/card_path.py",
		"scripts/new_card.py",
		"scripts/test_backfill_ids.py",
		"scripts/test_card_path.py",
		"scripts/test_new_card.py",
		"scripts/test_validate_cards.py",
		"scripts/validate_cards.py",
	}
	if got := files(t, res.Board); !slices.Equal(got, want) {
		t.Fatalf("board files:\n got  %v\n want %v", got, want)
	}
	if got := files(t, res.Docs); len(got) != 0 {
		t.Fatalf("docs starts empty, got %v", got)
	}

	// Byte for byte the template, not a copy that merely has the right names.
	for _, name := range want {
		fromTemplate, err := fs.ReadFile(templates.Board(), name)
		if err != nil {
			t.Fatal(err)
		}
		written, err := os.ReadFile(filepath.Join(res.Board, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(written) != string(fromTemplate) {
			t.Errorf("%s differs from the template", name)
		}
	}

	cards, err := board.Scan(res.Board)
	if err != nil {
		t.Fatalf("a created board must scan as a board: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("a created board is empty, got %d cards", len(cards))
	}
}

// The scripts are run as ./scripts/validate_cards.py by agents; embedding
// loses file modes, so Create must give them back. Every script in the
// directory is checked rather than a list of names: the board's scripts get
// added to, and a name left out of such a list is a script that reaches the
// operator without its executable bit and says so only when it is first run.
func TestCreateMakesTheScriptsExecutable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	res, err := Create(root, Options{InitRepo: noRepo})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(res.Board, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the created board has no scripts at all")
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".py") {
			continue
		}
		info, err := os.Stat(filepath.Join(res.Board, "scripts", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s is not executable: %v", entry.Name(), info.Mode())
		}
	}
	info, err := os.Stat(filepath.Join(res.Board, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Errorf("README.md must not be executable: %v", info.Mode())
	}
}

// "The same validator" is a claim about behaviour, so it is checked by running
// it: on the new, empty board it says there are no cards and exits zero.
func TestCreatedBoardRunsItsOwnValidator(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the validator cannot run here")
	}
	root := filepath.Join(t.TempDir(), "ws")
	res, err := Create(root, Options{InitRepo: noRepo})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "scripts/validate_cards.py")
	cmd.Dir = res.Board
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("validator failed on a new board: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "карточек не найдено") {
		t.Fatalf("validator on an empty board must say it found no cards, got: %s", out)
	}
}

func TestCreatePutsTheBoardUnderGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := filepath.Join(t.TempDir(), "ws")
	res, err := Create(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.RepoErr != nil {
		t.Fatalf("git is installed, so the board must be a repository: %v", res.RepoErr)
	}
	if _, err := os.Stat(filepath.Join(res.Board, ".git")); err != nil {
		t.Fatalf("the board is not a repository of its own: %v", err)
	}
}

// A board without git still works — the panel writes a field and says it could
// not commit it — so a failed git init is reported, not fatal: on a Mac with no
// developer tools, git is exactly what is missing.
func TestCreateReportsAFailedGitInitAndStillMakesTheBoard(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	gitGone := errors.New("git: no developer tools")
	res, err := Create(root, Options{InitRepo: func(string) error { return gitGone }})
	if err != nil {
		t.Fatalf("a missing git must not stop the board being created: %v", err)
	}
	if !errors.Is(res.RepoErr, gitGone) {
		t.Fatalf("the git failure must be reported: %v", res.RepoErr)
	}
	if _, err := board.Scan(res.Board); err != nil {
		t.Fatalf("the board must exist anyway: %v", err)
	}
}

// Somebody's board is never touched: a board directory that already holds
// files is kept exactly as it is, and so is a docs directory.
func TestCreateKeepsABoardAndDocsThatAlreadyHoldFiles(t *testing.T) {
	root := t.TempDir()
	cardsDir := filepath.Join(root, "board", "cards")
	if err := os.MkdirAll(cardsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(cardsDir, "mine.md")
	if err := os.WriteFile(card, []byte("---\nstage: new\n---\n# mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, "notes.md"), []byte("# notes\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Create(root, Options{InitRepo: func(string) error {
		t.Fatal("git init must not run on a board that was kept")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.BoardCreated || res.DocsCreated {
		t.Fatalf("existing parts must be reported as kept: %+v", res)
	}
	if got := files(t, filepath.Join(root, "board")); !slices.Equal(got, []string{"cards/mine.md"}) {
		t.Fatalf("an existing board was changed: %v", got)
	}
	if got := files(t, docs); !slices.Equal(got, []string{"notes.md"}) {
		t.Fatalf("an existing docs directory was changed: %v", got)
	}
}

// An empty board directory holds nobody's board, so it is filled — the case of
// a person who made the folder first and then pointed the panel at it.
func TestCreateFillsAnEmptyBoardDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "board"), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := Create(root, Options{InitRepo: noRepo})
	if err != nil {
		t.Fatal(err)
	}
	if !res.BoardCreated {
		t.Fatal("an empty board directory must be filled")
	}
	if _, err := board.Scan(res.Board); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTwiceChangesNothingTheSecondTime(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	if _, err := Create(root, Options{InitRepo: noRepo}); err != nil {
		t.Fatal(err)
	}
	before := files(t, root)
	res, err := Create(root, Options{InitRepo: noRepo})
	if err != nil {
		t.Fatal(err)
	}
	if res.BoardCreated || res.DocsCreated {
		t.Fatalf("a second run must keep what the first made: %+v", res)
	}
	if after := files(t, root); !slices.Equal(before, after) {
		t.Fatalf("a second run changed the workspace:\n before %v\n after  %v", before, after)
	}
}

// failingFS is a template one of whose files cannot be read: the copy fails
// part way through, after other files were already written.
type failingFS struct {
	fstest.MapFS
	broken string
}

func (f failingFS) Open(name string) (fs.File, error) {
	if name == f.broken {
		return nil, errors.New("read failed")
	}
	return f.MapFS.Open(name)
}

// ReadFile is overridden too: MapFS has its own, and fs.ReadFile would use it
// and never reach Open above.
func (f failingFS) ReadFile(name string) ([]byte, error) {
	if name == f.broken {
		return nil, errors.New("read failed")
	}
	return f.MapFS.ReadFile(name)
}

// A board appears whole or not at all. A half-copied board — a README and no
// validator — would pass for a board and fail the first agent that used it.
func TestCreateLeavesNoHalfBoardWhenTheCopyFails(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ws")
	tmpl := failingFS{
		MapFS: fstest.MapFS{
			"README.md":                 {Data: []byte("# board\n")},
			"cards/.gitkeep":            {Data: nil},
			"scripts/validate_cards.py": {Data: []byte("print()\n")},
		},
		broken: "scripts/validate_cards.py",
	}
	_, err := Create(root, Options{Template: tmpl, InitRepo: noRepo})
	if err == nil {
		t.Fatal("a template that cannot be read must fail the creation")
	}
	if _, statErr := os.Stat(filepath.Join(root, "board")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("a failed copy left a board directory behind: %v", statErr)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".board") {
			t.Fatalf("a failed copy left its staging directory behind: %s", e.Name())
		}
	}
}

func TestCreateRefusesARootThatIsAFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, Options{InitRepo: noRepo}); err == nil {
		t.Fatal("a root that is a file must be refused")
	}
}

func TestCreateRefusesARelativeRoot(t *testing.T) {
	if _, err := Create("fleetdeck", Options{InitRepo: noRepo}); err == nil {
		t.Fatal("a relative root would land wherever the panel was started from; it must be refused")
	}
}
