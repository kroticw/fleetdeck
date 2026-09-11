package supervisor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Swap puts the bundle built at staged in place at canonical, and the bundle
// that was at canonical at staged, in one step.
//
// The invariant it keeps is the operator's, in their words: «на каноническом
// пути всегда лежит рабочая версия, в любой момент» -- at the canonical path
// there is always a working version, at any moment. Replacing a bundle in two
// steps -- the old one out of the way, then the new one in -- leaves a moment
// with nothing there, and a Dock click, a login item or a crash in that moment
// finds no app at all. The exchange is one system call (renamex_np with
// RENAME_SWAP on macOS, renameat2 with RENAME_EXCHANGE on Linux), so there is
// no such moment; TestTheCanonicalPathIsNeverEmptyDuringASwap watches for one.
//
// staged and canonical must be on the same filesystem -- siblings, in
// practice. A first install has nothing at canonical to protect, and the
// staged bundle is simply moved in.
func Swap(staged, canonical string) error {
	if _, err := os.Lstat(canonical); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(staged, canonical); err != nil {
			return fmt.Errorf("move %s into place: %w", staged, err)
		}
		return nil
	}
	if err := exchange(staged, canonical); err != nil {
		return fmt.Errorf("swap %s into %s: %w", staged, canonical, err)
	}
	return nil
}
