package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/workspace"
)

// configPathOf is the configuration file init works on: the one the panel's
// setup passed, or ~/.config/fleetdeck/config.yaml under init's home.
func configPathOf(env initEnv) string {
	if env.config != "" {
		return env.config
	}
	return filepath.Join(env.home, ".config", "fleetdeck", "config.yaml")
}

// plannedFleet is the fleet `init --fleet` adds, and whether the
// configuration already has it — the same name on the same board, which a
// second identical run keeps rather than refuses. Everything that would
// refuse the fleet is an error here, before anything is made for it.
//
// A fleet is only ever added to a configuration that exists: the first fleet
// is the one init, or the panel's first launch, makes without --fleet.
func plannedFleet(cfgPath string, env initEnv) (fleet.Fleet, bool, error) {
	if env.workspace == "" && env.board == "" {
		return fleet.Fleet{}, false, errors.New("--fleet needs --workspace or --board: a fleet has a board of its own, and the default place is the first fleet's")
	}
	if _, err := os.Stat(cfgPath); errors.Is(err, fs.ErrNotExist) {
		return fleet.Fleet{}, false, fmt.Errorf("%s does not exist: the first fleet is the one init makes without --fleet, run that first", cfgPath)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fleet.Fleet{}, false, fmt.Errorf("%s does not load, and init adds no fleet to it: %w", cfgPath, err)
	}
	lay, err := chosenLayout(env)
	if err != nil {
		return fleet.Fleet{}, false, err
	}
	fl := fleet.Fleet{Name: env.fleet, BoardPath: lay.board}
	if lay.root != "" {
		fl.DocsPaths = []string{workspace.DocsDir(lay.root)}
	}
	for _, f := range cfg.FleetList() {
		if f.Name == fl.Name && filepath.Clean(f.BoardPath) == filepath.Clean(fl.BoardPath) {
			return fl, true, nil
		}
	}
	next := cfg
	next.Fleets = append(slices.Clone(cfg.Fleets), fl)
	if err := config.ValidateFleets(next); err != nil {
		return fleet.Fleet{}, false, fmt.Errorf("%s would refuse fleet %q: %w", cfgPath, fl.Name, err)
	}
	return fl, false, nil
}

// fleetSteps are init's steps for --fleet: the new fleet's board or
// workspace, then the fleet in the configuration — only over a board that
// exists, the same order a first run keeps — then the statusline and the
// agents' permission to write in the new folder.
func fleetSteps(cfgPath string, env initEnv) []initStep {
	cfgStep := initStep{name: "config"}
	fl, kept, err := plannedFleet(cfgPath, env)
	if err != nil {
		cfgStep.err = err
		return []initStep{cfgStep}
	}
	// The board step a first run takes for a configuration it just planned:
	// a workspace, or with --board a board alone, made or kept.
	boardStep, allow := ensureBoard(cfgPath, config.Config{BoardPath: fl.BoardPath}, true, nil, env)
	switch {
	case kept:
		cfgStep.note = fmt.Sprintf("%s (kept, fleet %s is already there)", cfgPath, fl.Name)
	case boardStep.err != nil:
		cfgStep.err = fmt.Errorf("fleet %s not added to %s: its board could not be made", fl.Name, cfgPath)
	default:
		if err := config.AddFleet(cfgPath, fl); err != nil {
			cfgStep.err = err
			break
		}
		cfgStep.note = fmt.Sprintf("%s (fleet %s added, board: %s)", cfgPath, fl.Name, fl.BoardPath)
		cfgStep.detail = "a running panel shows the new fleet after it is restarted"
	}
	return []initStep{boardStep, cfgStep, ensureStatusline(env), ensurePermissions(env, allow)}
}
