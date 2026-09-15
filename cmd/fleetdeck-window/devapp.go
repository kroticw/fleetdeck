//go:build darwin

package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	appconfig "github.com/kroticw/fleetdeck/internal/config"
)

// A dev app is this window built from a working tree by `make dev-app`, opened
// beside the installed app to try a change before it is released
// (docs/engineering/dev-app.md). It shares the operator's fleet daemon and
// board, and nothing else of the installed app's: it has an identifier, a port,
// a log and panel widths of its own, its panel runs on a copy of the operator's
// config, and it never updates, removes a leftover bundle, takes a panel over
// or stops a process that is not its own panel.

// Written in by `make dev-app` (-ldflags -X): devBuild is "true" for a dev
// app, and devURL is the URL it opens on when started with no -url, from
// Finder too. Every other build leaves both empty.
var (
	devBuild string
	devURL   string
)

// devBundleName is what `make dev-app` calls the bundle it builds.
const devBundleName = "fleetdeck-dev.app"

func isDevBuild() bool { return devBuild == "true" }

// startURL is the URL a window started with no -url opens on: a dev app's own
// when it has one, and otherwise the installed app's.
func startURL(dev bool, devURL string) string {
	if dev && devURL != "" {
		return devURL
	}
	return defaultURL
}

// panelPort is the port the panel is to listen on: the port of the URL the
// window looks at. Before, the panel took server.port from the config whatever
// the window looked at, and a window on another URL started a panel on the
// installed app's port. A URL that names no port is refused rather than
// guessed at.
func panelPort(panelURL string) (int, error) {
	u, err := url.Parse(panelURL)
	if err != nil {
		return 0, fmt.Errorf("the panel's URL %q: %w", panelURL, err)
	}
	p := u.Port()
	if p == "" {
		return 0, fmt.Errorf("the panel's URL %q names no port for the panel to listen on", panelURL)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("the panel's URL %q names no usable port", panelURL)
	}
	return port, nil
}

// devFlagsRefusal refuses a dev app started as an update's new window. A dev
// app never updates, so nothing of its own starts it that way, and a takeover
// would stop the panel on the port and have LaunchServices register the bundle
// it names.
func devFlagsRefusal(handover, canonical string) error {
	switch {
	case handover != "":
		return fmt.Errorf("a dev app is never started by an update, and this one was given --handover %q; refusing to start", handover)
	case canonical != "":
		return fmt.Errorf("a dev app is never started by an update, and this one was given --canonical %q; refusing to start", canonical)
	}
	return nil
}

// devPortRefusal refuses a dev app on the installed panel's port: server.port
// of the operator's config, or its default when there is no config.
func devPortRefusal(port int, operatorConfig string) error {
	cfg, err := appconfig.Load(operatorConfig)
	if err != nil {
		return fmt.Errorf("read the operator's config to keep off the installed panel's port: %w", err)
	}
	if port == cfg.ServerPort {
		return fmt.Errorf("port %d is the installed panel's (server.port in %s); a dev app takes a port of its own: make dev-app DEV_PORT=<port>", port, operatorConfig)
	}
	return nil
}

// devConfigPath is the copy of the operator's config a dev app's panel runs
// on.
func devConfigPath(home string) string {
	return filepath.Join(home, ".config", "fleetdeck", "dev", "config.yaml")
}

// copyDevConfig writes the operator's config to dev, with every banner off,
// over whatever an earlier dev app left there. What the dev panel writes --
// a pinned orchestrator, a session's name, a fleet -- goes to the copy and not
// to the file the installed panel reads and writes; two panels writing one
// file could lose each other's changes. The banners are the installed panel's
// to send. server.port is the dev app's port: the window hands its panel
// --port anyway, and a dev panel ever started without it still keeps off the
// installed panel's port.
func copyDevConfig(operatorConfig, dev string, port int) error {
	if _, err := os.Stat(operatorConfig); err != nil {
		return fmt.Errorf("a dev app runs its panel on a copy of the operator's config, and there is none to copy: %w", err)
	}
	cfg, err := appconfig.Load(operatorConfig)
	if err != nil {
		return fmt.Errorf("read the operator's config to copy it: %w", err)
	}
	cfg.Notify.Waiting, cfg.Notify.Failed, cfg.Notify.Silent, cfg.Notify.CardBlocked = false, false, false, false
	cfg.ServerPort = port
	if err := appconfig.Save(dev, cfg); err != nil {
		return fmt.Errorf("write the dev app's config %s: %w", dev, err)
	}
	return nil
}

// panelLogPath is the log the window's panels write to: the one the launch
// agent wrote the panel's output to, so a panel's history does not split in two
// at the day the window took over; a dev app's panel has its own.
func panelLogPath(home string, dev bool) string {
	name := "fleetdeck.log"
	if dev {
		name = "fleetdeck-dev.log"
	}
	return filepath.Join(home, "Library", "Logs", name)
}

// windowTitle says which of two open windows is the dev app.
func windowTitle(dev bool) string {
	if dev {
		return "fleetdeck dev"
	}
	return "fleetdeck"
}

// stopsOnly is the one binary a dev app's keeper may stop: its own panel's
// (supervisor.Keeper.StopsOnly). The app's keeper has no such limit.
func stopsOnly(exe string, dev bool) string {
	if !dev {
		return ""
	}
	return panelBinary(exe)
}
