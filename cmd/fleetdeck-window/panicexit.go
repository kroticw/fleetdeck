//go:build darwin

package main

import (
	"runtime"
)

// panicExit ends the window when a panic reaches main's deferred calls, before
// the web view's Destroy does.
//
// A panic inside a block on the main dispatch queue — a w.Dispatch callback, or
// Go that AppKit or WebKit calls on the main thread — unwinds through the C
// frames into main's deferred calls. The first of them to run used to be
// w.Destroy(), and webview's destructor posts a block to the main queue and
// turns the run loop until that block has run (deplete_run_loop_event_queue).
// The main queue is still inside the block that panicked, so it never runs:
// the window spun at full CPU with nothing in its log, and the panic, which Go
// prints only after the deferred calls, was never printed.
//
// Deferred after w.Destroy() it runs before it: the panic and every goroutine's
// stack go to the window's log, and the process exits with the status a panic
// exits with. Nothing is recovered into running on.
type panicExit struct {
	logf func(format string, args ...any)
	exit func(code int)
}

// guard is the deferred call. It must be deferred itself, not called from one:
// recover stops a panic only in the function the panic's deferred call is.
func (p panicExit) guard() {
	r := recover()
	if r == nil {
		return
	}
	p.logf("fleetdeck-window: panic on the main thread, the window exits: %v\n%s", r, allStacks())
	p.exit(2)
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
