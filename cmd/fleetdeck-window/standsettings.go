//go:build darwin

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
	// "newcard": the board opens its new card form as it loads, so a stand's
	// screenshot shows the form without anyone pressing its capsule.
	standOpenEnv = "FLEETDECK_STAND_OPEN"
	// "on": once its surfaces have loaded, the window enters full screen, and
	// after a while leaves it (standfullscreen.go), so a stand measures and
	// shows the frame in full screen and after it. v0.10.1's capsules lay over
	// the sessions panel in full screen, which no stand had entered.
	standFullScreenEnv = "FLEETDECK_STAND_FULLSCREEN"
	// "orchestrator", "sessions" or "both": the window opens with those panels
	// folded and the others unfolded, whatever the stand's defaults say, and
	// keeps no widths or folds (panelWidthsForStand, storesWidths). v0.10.2's
	// capsule row lay under the window's buttons beside the folded
	// orchestrator strip, which no stand had opened.
	standFoldEnv = "FLEETDECK_STAND_FOLD"
)

// standFold is how a stand said to fold the panels, "" off a stand or when it
// said nothing (main.go).
var standFold string

// panelWidthsForStand is the panels' widths and folds the window opens with:
// loaded from its defaults, folded as fold says when a stand said.
func panelWidthsForStand(loaded panelWidths, fold string) panelWidths {
	if fold == "" {
		return loaded
	}
	loaded.OrchestratorFolded = fold == "orchestrator" || fold == "both"
	loaded.SessionsFolded = fold == "sessions" || fold == "both"
	return loaded
}

// storesWidths is whether the window keeps the widths and folds its panels are
// given: not on a stand told how to fold, whose next window from the same
// defaults would otherwise open as this one's surfaces left it.
func storesWidths(fold string) bool { return fold == "" }

// standSettings is what a stand set; the zero value is a person's window.
type standSettings struct {
	panelStartTimeout time.Duration
	width, height     int
	appearance        string
	open              string
	fullScreen        bool
	fold              string
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
	if v, set := lookup(standOpenEnv); set {
		if v != "newcard" {
			return standSettings{}, fmt.Errorf("%s=%q is not newcard", standOpenEnv, v)
		}
		s.open = v
	}
	if v, set := lookup(standFullScreenEnv); set {
		if v != "on" {
			return standSettings{}, fmt.Errorf("%s=%q is not on", standFullScreenEnv, v)
		}
		s.fullScreen = true
	}
	if v, set := lookup(standFoldEnv); set {
		if v != "orchestrator" && v != "sessions" && v != "both" {
			return standSettings{}, fmt.Errorf("%s=%q is not orchestrator, sessions or both", standFoldEnv, v)
		}
		s.fold = v
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
