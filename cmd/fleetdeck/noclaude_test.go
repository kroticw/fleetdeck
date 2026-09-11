package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// TestMain puts a claude that refuses first on PATH, and empties the places
// looked at outside it, for every test in this package.
//
// This is not tidiness. A real `claude --bg` is a real session in the real
// fleet of whoever runs the tests: it reaches the machine's daemon whatever
// socket the panel was given, and with a test's own HOME it starts a daemon
// of its own, whose socket can sort ahead of the real one — and the panel,
// and every fleet tool, takes the first live socket there is. The mutation
// pass on this package did exactly that once: a mutant that let a stand look
// claude up found /opt/homebrew/bin/claude from a test that had set no PATH,
// and left a session called "оркестратор" and two daemons running on the
// operator's machine. Each test that means to run a claude gives its own; no
// test, and no mutant of the code under test, can reach the installed one.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "noclaude")
	if err != nil {
		fmt.Fprintln(os.Stderr, "noclaude:", err)
		os.Exit(2)
	}
	refuse := "#!/bin/sh\necho 'the real claude is hidden from these tests: give the test a claude of its own' >&2\nexit 97\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(refuse), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "noclaude:", err)
		os.Exit(2)
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	claudePlaces = nil
	orchestrator.SystemPlaces = nil
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
