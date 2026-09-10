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
	fmt.Fprintln(w, version.String())
}
