package templates

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The embedded template must be exactly the files of plugin/templates/board on
// disk. A file added to the template and forgotten in the go:embed line would
// otherwise reach boards made by upgrade-board.sh and never the ones the panel
// makes, and nothing else would notice.
func TestBoardEmbedsEveryTemplateFile(t *testing.T) {
	var onDisk []string
	err := filepath.WalkDir("board", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// What the template's own .gitignore keeps out of git: running the
		// validator's tests in place leaves a __pycache__ here, and it is not
		// part of the template.
		if d.Name() == "__pycache__" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() != ".DS_Store" {
			rel, _ := filepath.Rel("board", path)
			onDisk = append(onDisk, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var embedded []string
	err = fs.WalkDir(Board(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			embedded = append(embedded, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	slices.Sort(onDisk)
	slices.Sort(embedded)
	if !slices.Equal(onDisk, embedded) {
		t.Fatalf("embedded template differs from plugin/templates/board:\n on disk:  %v\n embedded: %v", onDisk, embedded)
	}
}

// Stated by name as well, so that a file vanishing from both places at once —
// which the comparison above cannot see — still fails here.
func TestBoardHoldsTheOperatorsBoardFiles(t *testing.T) {
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
	for _, name := range want {
		if _, err := fs.Stat(Board(), name); err != nil {
			t.Errorf("template lacks %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join("board", "cards", ".gitkeep")); err != nil {
		t.Fatalf("the on-disk template lost cards/.gitkeep: %v", err)
	}
}
