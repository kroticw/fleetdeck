//go:build darwin

package main

import "sync"

// windowUI is what of the window a takeover needs once the window is made.
// Each of handedOver and terminate runs on the UI thread.
type windowUI struct {
	dispatch func(func())
	// handedOver is the handover done: the panel's page asked for.
	handedOver func()
	// terminate closes the window, and with it this process.
	terminate func()
}

// windowGate holds back what needs the window until the window is made. A
// takeover begun before the window (handoverstart.go) hands it what the
// handover comes to, and goes on without waiting for it.
type windowGate struct {
	mu      sync.Mutex
	ui      *windowUI
	pending []func(windowUI)
}

// onUI runs f on the UI thread once the window is made: at once if it is, and
// otherwise, in order, when it is. It never waits for the window.
func (g *windowGate) onUI(f func(windowUI)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ui == nil {
		g.pending = append(g.pending, f)
		return
	}
	ui := *g.ui
	ui.dispatch(func() { f(ui) })
}

// beforeReady runs f, with the window kept from being made meanwhile, if the
// window is not made yet, and says whether it did.
func (g *windowGate) beforeReady(f func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ui != nil {
		return false
	}
	f()
	return true
}

// open is the window made: everything held back goes to the UI thread, in the
// order it came.
func (g *windowGate) open(ui windowUI) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ui = &ui
	for _, f := range g.pending {
		ui.dispatch(func() { f(ui) })
	}
	g.pending = nil
}
