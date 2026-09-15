// Command standpointer moves the pointer to the top of the main screen and away
// from it, for scripts/ci-window-stand.sh on a CI runner and nowhere else: in
// full screen a pointer at the top of the screen brings the title bar out over
// the window's content, which v0.10.2's dev build showed over the capsule row
// and which a stand cannot bring out any other way. On a person's machine the
// pointer is theirs, so it refuses unless GITHUB_ACTIONS is true; the stand's
// script checks the same before it runs this.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// result is where the pointer was asked to go and where it is after, in global
// display points from the main screen's top left, and whether this process may
// post events (CGPreflightPostEventAccess): a runner's TCC may not let it.
type result struct {
	access     bool
	askX, askY float64
	atX, atY   float64
}

func (r result) moved() bool {
	const slack = 1.0
	return r.atX-r.askX <= slack && r.askX-r.atX <= slack && r.atY-r.askY <= slack && r.askY-r.atY <= slack
}

var errNotOnRunner = errors.New("standpointer: refusing to move the pointer: GITHUB_ACTIONS is not true, and off a CI runner the pointer is a person's")

// mayMove is nil only on a CI runner.
func mayMove(lookup func(string) (string, bool)) error {
	if v, _ := lookup("GITHUB_ACTIONS"); v != "true" {
		return errNotOnRunner
	}
	return nil
}

// run moves the pointer to the top of the main screen or away from it, as to
// says, and says where it went; move is never called off a runner.
func run(to string, lookup func(string) (string, bool), move func(top bool) (result, error), out, errOut io.Writer) int {
	if err := mayMove(lookup); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 3
	}
	if to != "top" && to != "away" {
		_, _ = fmt.Fprintf(errOut, "standpointer: -to %q is neither top nor away\n", to)
		return 2
	}
	r, err := move(to == "top")
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 1
	}
	_, _ = fmt.Fprintf(out, "standpointer: post event access %v; the pointer asked to (%v, %v) is at (%v, %v)\n", r.access, r.askX, r.askY, r.atX, r.atY)
	if !r.moved() {
		return 1
	}
	return 0
}

func main() {
	to := flag.String("to", "", "top: the top middle of the main screen; away: its middle")
	flag.Parse()
	os.Exit(run(*to, os.LookupEnv, movePointer, os.Stdout, os.Stderr))
}
