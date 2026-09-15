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
	// Twice: coming out of full screen must leave the frame as it was, and
	// going in again must not bring back what the first time did not show.
	standFullScreenTrips = 2

	// While in full screen, timed from going in, before the window leaves: the
	// menu bar shown, as a pointer at the top of the screen shows it with the
	// title bar under it; the frame measured with it shown; the menu bar
	// hidden again. On the second trip the toolbar is hidden for a while
	// around that, to measure the frame without it.
	standRevealToolbarOff = 1 * time.Second
	standRevealShow       = 4 * time.Second
	standRevealMeasure    = 6 * time.Second
	standRevealHide       = 8 * time.Second
	standRevealToolbarOn  = 10 * time.Second
)

// trip is the trip into full screen the window is on, from 1.
func (s *standFullScreen) trip() int { return s.trips + 1 }

// standFullScreen is a stand's trips into full screen and out of it: v0.10.1's
// capsules lay over the sessions panel in full screen, which no stand had
// entered. The window enters it once both side surfaces have loaded, leaves it
// once it is in, and goes in again until it has made its trips.
type standFullScreen struct {
	on     bool
	loaded map[string]bool
	// asked: the first entry was asked for; in: the window is in full screen;
	// trips: how many times it came out.
	asked, in bool
	trips     int
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
// after the window is to leave it; once it is out with trips to go, how long
// after it is to go in again.
func (s *standFullScreen) changed(fullscreen bool) (time.Duration, bool) {
	if !s.on || !s.asked || s.trips >= standFullScreenTrips {
		return 0, false
	}
	switch {
	case fullscreen && !s.in:
		s.in = true
		return standFullScreenLeaveAfter, true
	case !fullscreen && s.in:
		s.in = false
		s.trips++
		if s.trips < standFullScreenTrips {
			return standFullScreenEnterAfter, true
		}
	}
	return 0, false
}
