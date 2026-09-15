//go:build darwin

package main

import (
	"path/filepath"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
	"github.com/kroticw/fleetdeck/internal/version"
)

// startInput is what the window knows of itself as it starts, before anything
// of it is made.
type startInput struct {
	// dev: this is a dev app (devapp.go).
	dev bool
	// url is the URL the window looks at (-url).
	url string
	// exe is this binary's path, and home the person's home directory.
	exe, home string
	// handover and canonical are what an update started this window with
	// (--handover, --canonical); empty for a window no update started.
	handover, canonical string
	// standSocket is a stand's daemon socket, empty off a stand.
	standSocket string
	// pid is this window's process.
	pid int
	// operatorConfig is the operator's config file, which a dev app keeps off
	// and runs its panel on a copy of.
	operatorConfig string
	// startTimeout is how long a panel the window starts has to answer.
	startTimeout time.Duration
	// env is the environment the window's panels are started with.
	env []string
}

// startPlan is what the window works out before anything of it is made. main
// takes each part of it as it is.
type startPlan struct {
	// keeper keeps the window's panel; OnEvent is main's to set. nil when the
	// start is refused.
	keeper *supervisor.Keeper
	// windowLog is where a dev app's window writes its own log, as well as to
	// stderr: opened from Finder or the Dock, its stderr goes nowhere anyone
	// reads. Set before anything can refuse the start, so a refusal is written
	// there too; empty for any other window.
	windowLog string
	// widthsSuite is where the panels' widths are kept (standwidths.go).
	widthsSuite string
	// title is the window's title and the app menu's name.
	title string
	// canonical is the installed bundle this window updates, if any.
	canonical string
	// way is what updateWay is asked, but for the team ID: that asks codesign,
	// and is worked out beside the start.
	way config
	// retiresLeftover: the start removes a bundle an earlier update left.
	retiresLeftover bool
}

// planStart works out the window's start from in, and refuses one that must
// not happen: a URL with no port, and for a dev app the installed panel's port
// or an update's handover. A dev app's config copy is written here, once the
// start is not refused.
func planStart(in startInput) (startPlan, error) {
	var plan startPlan
	if in.dev {
		plan.windowLog = filepath.Join(in.home, "Library", "Logs", "fleetdeck-dev-window.log")
	}
	port, err := panelPort(in.url)
	if err != nil {
		return plan, err
	}
	panelConfig := ""
	if in.dev {
		if err := devFlagsRefusal(in.handover, in.canonical); err != nil {
			return plan, err
		}
		if err := devPortRefusal(port, in.operatorConfig); err != nil {
			return plan, err
		}
		panelConfig = devConfigPath(in.home, port)
		if err := copyDevConfig(in.operatorConfig, panelConfig, legacyDevConfigPath(in.home), port); err != nil {
			return plan, err
		}
	}
	plan.canonical = canonicalBundle(in.exe, in.canonical)
	plan.keeper = &supervisor.Keeper{
		URL:  in.url,
		Bin:  panelBinary(in.exe),
		Args: panelArgs(in.pid, in.standSocket, port, panelConfig),
		// This window: its panels report it as their owner and go when it
		// goes, and a panel whose window is gone is replaced.
		Owner:        in.pid,
		Env:          in.env,
		LogPath:      panelLogPath(in.home, in.dev),
		StartTimeout: in.startTimeout,
		MinUptime:    launchdThrottle,
		Poll:         takenPanelPoll,
		// A dev app stops no process but its own panel's.
		StopsOnly: stopsOnly(in.exe, in.dev),
	}
	plan.widthsSuite = widthsSuite(in.standSocket, in.dev)
	plan.title = windowTitle(in.dev)
	plan.way = config{tree: treeDir, exe: in.exe, version: version.String(), canonical: in.canonical, dev: in.dev}
	plan.retiresLeftover = retiresLeftover(in.handover, plan.canonical, in.dev)
	return plan, nil
}
