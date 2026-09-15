//go:build darwin

package main

import "time"

// standFullScreenOn is a stand's FLEETDECK_STAND_FULLSCREEN (standsettings.go),
// set by main.go; false in a person's window.
var standFullScreenOn bool

const (
	// Long enough for the orchestrator's terminal to write its first screen
	// before the window goes into full screen, and for a stand to measure and
	// take the frame in it before the window comes back.
	standFullScreenEnterAfter = 6 * time.Second
	standFullScreenLeaveAfter = 12 * time.Second
)

// standFullScreen is a stand's one trip into full screen and out of it: v0.10.1's
// capsules lay over the sessions panel in full screen, which no stand had
// entered. The window enters it once both side surfaces have loaded, and
// leaves it once it is in.
type standFullScreen struct {
	on     bool
	loaded map[string]bool
	// asked: entering was asked for; in: the window went in; done: it came out.
	asked, in, done bool
}

func newStandFullScreen(on bool) *standFullScreen {
	return &standFullScreen{on: on, loaded: map[string]bool{}}
}

// surfaceLoaded is a side surface's page saying it is a panel: once both have,
// how long after the window is to enter full screen.
func (s *standFullScreen) surfaceLoaded(surface string) (time.Duration, bool) {
	if !s.on || s.asked {
		return 0, false
	}
	s.loaded[surface] = true
	for _, side := range sideSurfaces {
		if !s.loaded[side] {
			return 0, false
		}
	}
	s.asked = true
	return standFullScreenEnterAfter, true
}

// changed is the window's full screen as it now is: once it is in, how long
// after the window is to leave it.
func (s *standFullScreen) changed(fullscreen bool) (time.Duration, bool) {
	if !s.on || !s.asked || s.done {
		return 0, false
	}
	switch {
	case fullscreen && !s.in:
		s.in = true
		return standFullScreenLeaveAfter, true
	case !fullscreen && s.in:
		s.done = true
	}
	return 0, false
}
