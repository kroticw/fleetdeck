package main

import (
	"fmt"
	"io"

	"github.com/kroticw/fleetdeck/internal/version"
)

// runVersion writes the build version.
//
// It takes a writer rather than printing directly so the behaviour is testable
// without capturing os.Stdout, and it is the single implementation behind both ways
// of asking: the `version` subcommand and the older `--version` flag. Two questions
// with one answer must not have two implementations to drift apart — the release
// verification compares what the binary prints against the tag it was built from, and
// it can only check one of them at a time.
func runVersion(w io.Writer) {
	// The write error is dropped on purpose. This prints one short line, and the only
	// ways it can fail are a destination that has gone away or filled up — a closed
	// pipe from `fleetdeck version | head -1`, most often — where there is no longer
	// anywhere to report the failure to. Returning it would also give this function a
	// second thing to be, and its whole point is that both ways of asking for the
	// version share one implementation with one behaviour.
	_, _ = fmt.Fprintln(w, version.String())
}
