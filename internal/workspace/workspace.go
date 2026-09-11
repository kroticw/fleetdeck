// Package workspace creates the directory a fleet keeps its board and its
// documentation in: <root>/board, a git repository laid out like the board
// template (plugin/templates/board), and <root>/docs, which the panel's
// documentation section reads.
//
// The configuration file stays where it is (~/.config/fleetdeck/config.yaml)
// and names both directories; nothing here writes it.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/plugin/templates"
)

const (
	boardName = "board"
	docsName  = "docs"

	dirMode    fs.FileMode = 0o700
	fileMode   fs.FileMode = 0o600
	scriptMode fs.FileMode = 0o700
)

// BoardDir is where a workspace keeps its board.
func BoardDir(root string) string { return filepath.Join(root, boardName) }

// DocsDir is where a workspace keeps its documentation.
func DocsDir(root string) string { return filepath.Join(root, docsName) }

// Options are the two things a test replaces. The zero value is the real
// template and the real git.
type Options struct {
	// Template is the board to copy; nil is templates.Board().
	Template fs.FS
	// InitRepo puts a new board under git; nil is board.InitRepo.
	InitRepo func(dir string) error
}

// Result is what Create found and did.
type Result struct {
	Root, Board, Docs string
	// BoardCreated and DocsCreated are false for a part that already existed
	// and was kept as it was.
	BoardCreated, DocsCreated bool
	// RepoErr is why a board Create made is not under git. The board works
	// without git — the panel then writes a field and says it could not commit
	// it — so this is reported rather than returned: on a Mac with no
	// developer tools, git is exactly what is missing.
	RepoErr error
}

// Create makes the workspace at root, which must be absolute.
//
// A board directory that already holds anything is somebody's board and is not
// touched, and neither is an existing docs directory. A new board appears whole
// or not at all: it is built in a staging directory beside its final place and
// renamed into it, so a copy that fails part way leaves nothing that looks like
// a board.
func Create(root string, opt Options) (Result, error) {
	if !filepath.IsAbs(root) {
		return Result{}, fmt.Errorf("workspace path %q is not absolute", root)
	}
	root = filepath.Clean(root)
	if opt.Template == nil {
		opt.Template = templates.Board()
	}
	if opt.InitRepo == nil {
		opt.InitRepo = board.InitRepo
	}

	res := Result{Root: root, Board: BoardDir(root), Docs: DocsDir(root)}
	if err := os.MkdirAll(root, dirMode); err != nil {
		return res, fmt.Errorf("create workspace %s: %w", root, err)
	}

	var err error
	res.BoardCreated, res.RepoErr, err = CreateBoard(res.Board, opt)
	if err != nil {
		return res, err
	}

	switch _, err := os.Stat(res.Docs); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(res.Docs, dirMode); err != nil {
			return res, fmt.Errorf("create docs %s: %w", res.Docs, err)
		}
		res.DocsCreated = true
	case err != nil:
		return res, fmt.Errorf("read docs %s: %w", res.Docs, err)
	}
	return res, nil
}

// CreateBoard makes a board at dir, which must be absolute, when dir is absent
// or empty, and reports whether it did. A dir that holds anything is kept as it
// is. repoErr is why a board it made is not under git (see Result.RepoErr).
// Create calls it for a workspace's board; `fleetdeck init --board` calls it
// for a board with no workspace around it.
func CreateBoard(dir string, opt Options) (created bool, repoErr error, err error) {
	if !filepath.IsAbs(dir) {
		return false, nil, fmt.Errorf("board path %q is not absolute", dir)
	}
	dir = filepath.Clean(dir)
	if opt.Template == nil {
		opt.Template = templates.Board()
	}
	if opt.InitRepo == nil {
		opt.InitRepo = board.InitRepo
	}
	empty, err := emptyOrAbsent(dir)
	if err != nil || !empty {
		return false, nil, err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, dirMode); err != nil {
		return false, nil, fmt.Errorf("create %s: %w", parent, err)
	}
	if err := placeBoard(parent, dir, opt.Template); err != nil {
		return false, nil, err
	}
	return true, opt.InitRepo(dir), nil
}

// emptyOrAbsent reports whether dir holds nothing at all. A path that exists and
// is not a directory is an error, not something to build over.
func emptyOrAbsent(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("read %s: %w", dir, err)
	}
	return len(entries) == 0, nil
}

// placeBoard copies the template into a staging directory under parent and
// renames it to dest. An existing dest is an empty directory (Create checked);
// it is removed first, because os.Rename on Unix refuses to replace a
// directory even where rename(2) itself would. os.Remove removes only an empty
// directory, so a file that arrived there in between fails this rather than
// being lost.
func placeBoard(parent, dest string, tmpl fs.FS) error {
	staging, err := os.MkdirTemp(parent, "."+filepath.Base(dest)+".tmp-")
	if err != nil {
		return fmt.Errorf("stage board: %w", err)
	}
	placed := false
	defer func() {
		if !placed {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := os.Chmod(staging, dirMode); err != nil {
		return fmt.Errorf("stage board: %w", err)
	}
	if err := copyTemplate(tmpl, staging); err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("place board at %s: %w", dest, err)
	}
	if err := os.Rename(staging, dest); err != nil {
		return fmt.Errorf("place board at %s: %w", dest, err)
	}
	placed = true
	return nil
}

// copyTemplate writes every file of tmpl under dir. An embedded file carries no
// mode, so the scripts are made executable here, as bootstrap-board.sh did.
func copyTemplate(tmpl fs.FS, dir string) error {
	return fs.WalkDir(tmpl, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read board template: %w", err)
		}
		target := filepath.Join(dir, filepath.FromSlash(path))
		if d.IsDir() {
			if path == "." {
				return nil
			}
			return os.Mkdir(target, dirMode)
		}
		data, err := fs.ReadFile(tmpl, path)
		if err != nil {
			return fmt.Errorf("read board template %s: %w", path, err)
		}
		mode := fileMode
		if strings.HasPrefix(path, "scripts/") && strings.HasSuffix(path, ".py") {
			mode = scriptMode
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	})
}
