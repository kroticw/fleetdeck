// Package templates carries the board template into the fleetdeck binary, so a
// new workspace gets the same board the operator runs today — the same
// validator, the same README, the same archive — from the panel itself, with no
// checkout of this repository and no shell script beside it.
//
// The directive lives here, next to the files, for the reason web/embed.go
// gives: a go:embed pattern cannot climb out of its own package directory.
package templates

import (
	"embed"
	"io/fs"
)

// board holds plugin/templates/board. The two dot-files are named explicitly:
// directory walking skips names starting with "." (see web/embed.go on why
// "all:" is not used), and both are part of the template — .gitignore keeps
// caches out of the board's history, and cards/.gitkeep keeps an empty cards/
// in git, which is what makes an empty board a board (internal/board.Scan).
//
//go:embed board/README.md board/.gitignore board/cards/.gitkeep board/archive board/scripts
var board embed.FS

// Board returns the board template rooted at the board directory itself:
// "README.md", "cards/.gitkeep", "scripts/validate_cards.py", and so on.
func Board() fs.FS {
	sub, err := fs.Sub(board, "board")
	if err != nil {
		// fs.Sub fails only on an invalid path, and "board" is a constant.
		panic(err)
	}
	return sub
}
