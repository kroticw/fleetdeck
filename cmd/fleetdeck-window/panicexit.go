//go:build darwin

package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// panicExit ends the window when a panic reaches a guard: says what panicked,
// keeps it in a file, and exits with the status a panic exits with. Nothing is
// recovered into running on.
//
// On the main thread the guard is deferred right after the web view's
// Destroy, so it runs before it. A panic inside a block on the main dispatch
// queue — a w.Dispatch callback, a binding, or Go that AppKit or WebKit calls
// on the main thread — unwinds through the C frames into main's deferred
// calls. The first of them to run used to be w.Destroy(), and webview's
// destructor posts a block to the main queue and turns the run loop until that
// block has run (deplete_run_loop_event_queue). The main queue is still inside
// the block that panicked, so it never runs: the window spun at full CPU with
// nothing in its log, and the panic, which Go prints only after the deferred
// calls, was never printed.
//
// Every goroutine the window starts begins with a guard too. A panic there
// always ended the process at once; the guard keeps what it was, since from the
// Dock or Finder the window's stderr is /dev/null.
type panicExit struct {
	logf func(format string, args ...any)
	exit func(code int)
	// file is where the panic is kept for good, "" for nowhere: the installed
	// app keeps no window log, and a panic said only to its stderr is lost.
	file  string
	build build
	exe   string
	now   func() time.Time
	// where says which part of the window panicked, as the log line reads:
	// "on the main thread", "in the keeper's goroutine".
	where string
}

// panics is the window's own guard, set at the start of main before any
// goroutine starts and only read after. Its zero value says the panic to the
// standard logger, keeps no file, and exits.
var panics panicExit

// panicRecorded holds the panic file to one record a process.
var panicRecorded atomic.Bool

// panicMu is taken by the first guard a panic reaches and never let go. A
// second panic meanwhile, on another goroutine, waits on it instead of exiting
// while the first is still keeping its record, which would end the process
// with the record cut short or not written.
var panicMu sync.Mutex

// panicFileLimit is how large the panic file grows before the next record sets
// it aside as .1, over the one set aside before.
const panicFileLimit = 1 << 20

// in is p for the part of the window named by where.
func (p panicExit) in(where string) panicExit {
	p.where = where
	return p
}

// guard is the deferred call. It must be deferred itself, not called from one:
// recover stops a panic only in the deferred function the panic runs. A
// goroutine that ends by returning or by runtime.Goexit passes it untouched.
func (p panicExit) guard() {
	r := recover()
	if r == nil {
		return
	}
	// Never unlocked: the process ends under it (panicMu).
	panicMu.Lock()
	logf, exit, where := p.logf, p.exit, p.where
	if logf == nil {
		logf = log.Printf
	}
	if exit == nil {
		exit = os.Exit
	}
	if where == "" {
		where = "in the window"
	}
	stacks := allStacks()
	logf("fleetdeck-window: panic %s, the window exits: %v\n%s", where, r, stacks)
	if err := p.record(r, stacks); err != nil {
		logf("fleetdeck-window: the panic could not be kept in %q: %v", p.file, err)
	}
	exit(2)
}

// record appends the panic to p.file: when, which build and binary, where,
// what, and every goroutine's stack. A process keeps one record, however many
// of its goroutines panic on the way out. Keeping it never panics: whatever
// goes wrong is returned.
func (p panicExit) record(r any, stacks []byte) (err error) {
	if p.file == "" {
		return errors.New("the window has no panic file")
	}
	if !panicRecorded.CompareAndSwap(false, true) {
		return nil
	}
	defer func() {
		if x := recover(); x != nil {
			err = fmt.Errorf("keeping it panicked: %v", x)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(p.file), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(p.file); err == nil && info.Size() > panicFileLimit {
		// Set aside rather than cut: the record is still kept if this fails.
		_ = os.Rename(p.file, p.file+".1")
	}
	f, err := os.OpenFile(p.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	now := time.Now
	if p.now != nil {
		now = p.now
	}
	revision := p.build.Revision
	switch {
	case revision == "":
		revision = "unknown"
	case p.build.Modified:
		revision += " (modified)"
	}
	_, werr := fmt.Fprintf(f, "=== %s fleetdeck-window panic\nversion: %s\nrevision: %s\nbinary: %s\nwhere: %s\npanic: %v\n\n%s\n",
		now().Format(time.RFC3339Nano), p.build.Version, revision, p.exe, p.where, r, stacks)
	return errors.Join(werr, f.Close())
}

// panicLogPath is where the window keeps a panic, beside the logs: a dev app
// keeps its own, as it keeps its own window log, so a dev build's panic is
// never read as the installed app's.
func panicLogPath(home string, dev bool) string {
	name := "fleetdeck-window-panic.log"
	if dev {
		name = "fleetdeck-dev-window-panic.log"
	}
	return filepath.Join(home, "Library", "Logs", name)
}

// allStacks is every goroutine's stack, the panicking one first: while a
// deferred call runs, the stack it panicked from is still there to be read.
func allStacks() []byte {
	buf := make([]byte, 64<<10)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}
