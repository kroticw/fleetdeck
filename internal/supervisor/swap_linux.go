package supervisor

import "golang.org/x/sys/unix"

// The panel ships for macOS only; this is here so the package, and the test
// that watches the canonical path during a swap, run on the Linux CI leg too.
func exchange(a, b string) error {
	return unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
}
