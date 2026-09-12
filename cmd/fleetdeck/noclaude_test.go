package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// TestMain puts a claude that refuses first on PATH, empties the places
// looked at outside it, and points the job store at an empty directory, for
// every test in this package.
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
//
// The job store is hidden for the same kind of reason, one step milder.
// Collect reads ~/.claude/jobs to find the sessions the daemon no longer
// lists (see internal/jobs), and that directory on the machine running the
// tests holds every background session its owner has ever started: a test
// that hands a fake daemon two sessions and counts what comes back got
// thirty-four, and would have gone on getting a different number every day.
// Nothing here writes to the store, so the cost was only ever noise — but
// noise that makes a test's result depend on whose laptop it runs on, which
// is the same defect as reaching the real claude, just quieter. Each test
// that means to read a store points jobStoreDir at one it built.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "noclaude")
	if err != nil {
		fmt.Fprintln(os.Stderr, "noclaude:", err)
		os.Exit(2)
	}
	emptyStore, err := os.MkdirTemp("", "nojobstore")
	if err != nil {
		fmt.Fprintln(os.Stderr, "noclaude:", err)
		os.Exit(2)
	}
	jobStoreDir = emptyStore
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
	_ = os.RemoveAll(emptyStore)
	os.Exit(code)
}
