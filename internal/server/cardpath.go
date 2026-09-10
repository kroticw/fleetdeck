package server

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

// errOutsideBoard is what confineToBoard returns for a path that does not resolve
// to somewhere inside the board directory, whether it climbed out with .., named
// somewhere else outright, or went through a symlink that pointed away.
var errOutsideBoard = errors.New("path is outside the board directory")

// confineToBoard turns the path a card write arrived with into an absolute path
// inside boardDir, or refuses it.
//
// The path comes from the browser and board.SetField will rewrite a stage: or
// progress: line in any file that has frontmatter — another vault, a cloned
// repository, a checked-out spec. Confinement is the only thing standing between
// that function and the rest of the disk, so it happens here, where the untrusted
// path arrives.
//
// Both sides are resolved with EvalSymlinks before they are compared, because a
// string prefix proves nothing: a symlink inside the board pointing out of it
// passes any prefix test while writing somewhere else entirely. Resolving one
// side only is just as wrong in the other direction — on macOS /tmp is itself a
// symlink to /private/tmp, so a resolved path under an unresolved board directory
// looks foreign when it is not.
//
// A relative path is taken as relative to the board, which is the only place a
// card can be. It is never resolved against the process's working directory,
// which has nothing to do with where the board is.
func confineToBoard(boardDir, path string) (string, error) {
	realBoard, err := filepath.EvalSymlinks(boardDir)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(realBoard, path)
	}
	resolved, err := resolveExisting(path)
	if err != nil {
		// The path cannot be resolved, so it cannot be shown to be inside the
		// board. A write that cannot be confined does not happen.
		return "", errOutsideBoard
	}
	if !strings.HasPrefix(resolved, realBoard+string(filepath.Separator)) {
		return "", errOutsideBoard
	}
	return resolved, nil
}

// resolveExisting resolves the symlinks in path, allowing the tail of it not to
// exist yet.
//
// filepath.EvalSymlinks refuses a path that is not there, and a card that is not
// there still has to be placed on one side of the board's boundary or the other:
// a missing card inside the board is a 404 the board itself reports, while a
// missing path outside it is refused here and never reaches the board at all.
// Resolving the deepest part that does exist and appending the rest keeps that
// distinction without letting an unresolved component smuggle a symlink past the
// comparison — every component that exists, and so every component that could be
// a link, is resolved.
func resolveExisting(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolvedParent, parentErr := resolveExisting(parent)
	if parentErr != nil {
		return "", parentErr
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), nil
}
