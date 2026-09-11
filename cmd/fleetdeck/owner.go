package main

import (
	"fmt"
	"os"
)

// A panel lives exactly as long as the window that started it -- the
// operator's rule, 2026-09-11: "with fleetdeck the panel goes out, with its
// start it starts". Quitting the app, the app crashing, the app killed with
// SIGKILL: the panel goes each time, and the next window starts a fresh one
// from its own bundle. A panel left behind by a window is how an old build
// ended up answering a new window.
//
// The panel keeps the rule itself, so that no way of the window ending is
// missed: started with --owner-pid, it checks that the owner is its parent --
// the window starts it directly, and a process in between would leave it
// watching the wrong one -- and then asks the kernel to be told when that
// process exits (kqueue on macOS, a pidfd on Linux), rather than polling. The
// exit is then the same graceful shutdown a SIGTERM gets.
//
// A window hiding itself (the red button) is not the window ending: the
// process is there, and so is the panel. A panel started from a terminal has
// no owner and is not affected.

// checkOwner refuses an owner that is not this process's parent.
func checkOwner(owner int) error {
	if parent := os.Getppid(); parent != owner {
		return fmt.Errorf("started with --owner-pid %d, but this panel's parent is %d: the window starts its panel directly, and a panel watching a process that is not its parent would outlive the window", owner, parent)
	}
	return nil
}
