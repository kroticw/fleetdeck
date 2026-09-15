//go:build darwin

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	appconfig "github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
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

// devPortRefusal refuses a dev app on a port the installed panel may be on:
// the port the installed app hands its panel, its default URL's, and
// server.port of the operator's config -- its default when there is no config
// -- where a panel started without --port listens.
func devPortRefusal(port int, operatorConfig string) error {
	installed, err := panelPort(defaultURL)
	if err != nil {
		return err
	}
	cfg, err := appconfig.Load(operatorConfig)
	if err != nil {
		return fmt.Errorf("read the operator's config to keep off the installed panel's port: %w", err)
	}
	switch port {
	case installed:
		return fmt.Errorf("port %d is the one the installed app hands its panel; a dev app takes a port of its own: make dev-app DEV_PORT=<port>", port)
	case cfg.ServerPort:
		return fmt.Errorf("port %d is server.port in %s, where a panel started without --port listens; a dev app takes a port of its own: make dev-app DEV_PORT=<port>", port, operatorConfig)
	}
	return nil
}

// devConfigPath is the copy of the operator's config the panel of a dev app on
// port runs on: one for each port, so dev apps from two trees on two ports do
// not write over each other's.
func devConfigPath(home string, port int) string {
	return filepath.Join(home, ".config", "fleetdeck", "dev", strconv.Itoa(port), "config.yaml")
}

// legacyDevConfigPath is the one config copy every dev app shared before the
// copies were kept per port.
func legacyDevConfigPath(home string) string {
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
//
// One thing of the previous copy is kept: the fleets made from a dev panel,
// which asks to be restarted to work in a new fleet. The previous copy is
// dev's own, or legacy's when dev has none yet. Of its listed fleets, one is
// carried into the new copy when:
//
//   - no fleet of the operator's config has its name now -- the operator's of
//     the same name is the one kept;
//   - it was made in a dev app: not among the operator's fleets the previous
//     copy was made with (copiedFleetsPath), so a fleet the operator removed
//     or renamed since is not brought back. A copy with no such record -- the
//     shared copy of before -- tells nothing of which were the operator's, and
//     of its fleets only those on a board no fleet of the operator's config is
//     on are taken;
//   - the copy would still load with it: a fleet on a board or with an
//     orchestrator the operator's config has taken since is left out, and a
//     note says so.
//
// Nothing else of the previous copy is kept. The notes are for the window's
// log.
func copyDevConfig(operatorConfig, dev, legacy string, port int) ([]string, error) {
	if _, err := os.Stat(operatorConfig); err != nil {
		return nil, fmt.Errorf("a dev app runs its panel on a copy of the operator's config, and there is none to copy: %w", err)
	}
	cfg, err := appconfig.Load(operatorConfig)
	if err != nil {
		return nil, fmt.Errorf("read the operator's config to copy it: %w", err)
	}
	previous, err := previousDevCopy(dev, legacy)
	if err != nil {
		return nil, err
	}
	operators := cfg.FleetList()
	named, boards := map[string]bool{}, map[string]bool{}
	for _, f := range operators {
		named[f.Name] = true
		if f.BoardPath != "" {
			boards[filepath.Clean(f.BoardPath)] = true
		}
	}
	var notes []string
	for _, f := range previous.fleets {
		switch {
		case named[f.Name]:
			continue
		case previous.copied != nil && previous.copied[f.Name]:
			continue
		case previous.copied == nil && boards[filepath.Clean(f.BoardPath)]:
			continue
		}
		next := cfg
		next.Fleets = append(slices.Clone(cfg.Fleets), f)
		if err := appconfig.ValidateFleets(next); err != nil {
			notes = append(notes, fmt.Sprintf("fleet %q of the previous dev config copy %s is not kept: %v", f.Name, previous.path, err))
			continue
		}
		cfg = next
		named[f.Name] = true
	}
	cfg.Notify.Waiting, cfg.Notify.Failed, cfg.Notify.Silent, cfg.Notify.CardBlocked = false, false, false, false
	cfg.ServerPort = port
	if err := appconfig.Save(dev, cfg); err != nil {
		return notes, fmt.Errorf("write the dev app's config %s: %w", dev, err)
	}
	names := make([]string, 0, len(operators))
	for _, f := range operators {
		names = append(names, f.Name)
	}
	record, err := json.Marshal(names)
	if err != nil {
		return notes, err
	}
	if err := os.WriteFile(copiedFleetsPath(dev), record, 0o600); err != nil {
		return notes, fmt.Errorf("record the operator's fleets beside the dev app's config %s: %w", dev, err)
	}
	return notes, nil
}

// copiedFleetsPath is where a config copy's record of the operator's fleets it
// was made with is kept: beside the copy, since config.yaml takes no key it
// does not know.
func copiedFleetsPath(copyPath string) string {
	return filepath.Join(filepath.Dir(copyPath), "copied-fleets.json")
}

// previousCopy is the previous config copy's listed fleets, where it is, and
// the names of the operator's fleets it was made with -- nil when it keeps no
// record of them.
type previousCopy struct {
	path   string
	fleets []fleet.Fleet
	copied map[string]bool
}

// previousDevCopy is dev when it is there, and otherwise legacy when that is;
// nothing when neither is. Listed fleets only: a fleet a panel adds goes to
// the fleets list, and a copy's top-level fleet is the operator's as it was
// when that copy was made.
func previousDevCopy(dev, legacy string) (previousCopy, error) {
	for _, path := range []string{dev, legacy} {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		cfg, err := appconfig.Load(path)
		if err != nil {
			return previousCopy{}, fmt.Errorf("the dev app's previous config copy %s does not load, so the fleets made in it cannot be kept; remove it to start from the operator's config: %w", path, err)
		}
		previous := previousCopy{path: path, fleets: cfg.Fleets}
		if data, err := os.ReadFile(copiedFleetsPath(path)); err == nil {
			var names []string
			if err := json.Unmarshal(data, &names); err != nil {
				return previousCopy{}, fmt.Errorf("the record %s beside the dev app's previous config copy does not read; remove it: %w", copiedFleetsPath(path), err)
			}
			previous.copied = map[string]bool{}
			for _, name := range names {
				previous.copied[name] = true
			}
		}
		return previous, nil
	}
	return previousCopy{}, nil
}

// devWindowLogPath is where a dev app's window writes its own log.
func devWindowLogPath(home string) string {
	return filepath.Join(home, "Library", "Logs", "fleetdeck-dev-window.log")
}

// openDevWindowLog has the window's log, and what the flag package says of the
// command line, written to the dev app's own log as well as to stderr, from
// before the flags are parsed. Opened from Finder or the Dock, a dev app's
// stderr goes nowhere anyone reads, and why it refused to start would go with
// it.
func openDevWindowLog() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("fleetdeck-window: no home directory to keep the dev app's log in: %v", err)
		return
	}
	path := devWindowLogPath(home)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("fleetdeck-window: the dev app's log %s cannot be opened: %v", path, err)
		return
	}
	w := io.MultiWriter(os.Stderr, f)
	log.SetOutput(w)
	flag.CommandLine.SetOutput(w)
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
