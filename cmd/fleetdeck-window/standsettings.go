package main

import (
	"fmt"
	"strings"
	"time"
)

// On a stand, and only there, the window takes a few settings from its
// environment that a person's window never has. A variable set without the
// stand's socket (standSocketEnv) is not read at all: a person's window cannot
// be given another deadline, size or appearance by whatever happens to be in
// the environment it was started with.
const (
	// How long the panel has to answer, as a Go duration: a stand measuring
	// a panel's real start lets it take longer than a person's window would.
	standPanelStartTimeoutEnv = "FLEETDECK_STAND_PANEL_START_TIMEOUT"
	// The window's size in points, WIDTHxHEIGHT: a runner's screen is smaller
	// than the window, and a screenshot of a window past the screen's edge
	// shows neither panel whole.
	standWindowSizeEnv = "FLEETDECK_STAND_WINDOW_SIZE"
	// "light" or "dark", in place of the system's appearance: a runner's system
	// appearance cannot be relied on to reach a window started after it
	// changed.
	standAppearanceEnv = "FLEETDECK_STAND_APPEARANCE"
	// "1": the panel is started before the window's web views. TEMPORARY
	// (T-056, v0.10.1): for one measurement of whether starting the panel
	// alongside WebKit's own processes is what makes it slow under load.
	standPanelFirstEnv = "FLEETDECK_STAND_PANEL_FIRST"
)

// standSettings is what a stand set; the zero value is a person's window.
type standSettings struct {
	panelStartTimeout time.Duration
	width, height     int
	appearance        string
	panelFirst        bool
}

// Smaller than this the frame has no room for both panels and the board.
const (
	standMinWidth  = 800
	standMinHeight = 500
)

func standSettingsFrom(standSocket string, lookup func(string) (string, bool)) (standSettings, error) {
	var s standSettings
	if standSocket == "" {
		return s, nil
	}
	if v, set := lookup(standPanelStartTimeoutEnv); set {
		d, err := time.ParseDuration(strings.TrimSpace(v))
		if err != nil || d <= 0 {
			return standSettings{}, fmt.Errorf("%s=%q is not a positive duration such as 10s", standPanelStartTimeoutEnv, v)
		}
		s.panelStartTimeout = d
	}
	if v, set := lookup(standWindowSizeEnv); set {
		var w, h int
		if n, err := fmt.Sscanf(strings.TrimSpace(v), "%dx%d", &w, &h); err != nil || n != 2 || w < standMinWidth || h < standMinHeight {
			return standSettings{}, fmt.Errorf("%s=%q is not a size such as 1000x700, at least %dx%d", standWindowSizeEnv, v, standMinWidth, standMinHeight)
		}
		s.width, s.height = w, h
	}
	if v, set := lookup(standAppearanceEnv); set {
		if v != "light" && v != "dark" {
			return standSettings{}, fmt.Errorf("%s=%q is neither light nor dark", standAppearanceEnv, v)
		}
		s.appearance = v
	}
	if v, set := lookup(standPanelFirstEnv); set {
		if v != "1" && v != "0" {
			return standSettings{}, fmt.Errorf("%s=%q is neither 1 nor 0", standPanelFirstEnv, v)
		}
		s.panelFirst = v == "1"
	}
	return s, nil
}

// startTimeout is how long the panel has to answer in this window.
func (s standSettings) startTimeout() time.Duration {
	if s.panelStartTimeout > 0 {
		return s.panelStartTimeout
	}
	return panelStartTimeout
}

// size is the window's size in points.
func (s standSettings) size() (width, height int) {
	if s.width > 0 {
		return s.width, s.height
	}
	return 1440, 900
}
