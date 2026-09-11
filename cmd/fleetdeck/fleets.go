package main

import (
	"fmt"
	"slices"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/server"
)

// newFleets is server.Deps.Fleet: for a fleet named in a request, its board,
// documentation, card start, orchestrator pin and orchestrator wizard.
//
// The fleets are the ones the panel started with; a fleet added while it runs
// (init --fleet) is served after a restart, which init says. Each fleet's
// wizard is made once, here, because an appointer holds the one-at-a-time
// lock that keeps two appointments of the same fleet from racing.
func newFleets(o runOpts, cfg config.Config, dc *daemon.Client, collector *Collector) func(name string) (server.FleetDeps, error) {
	fleets := cfg.FleetList()
	wizards := map[string]*orchestrator.Appointer{}
	for _, f := range fleets {
		wizards[f.Name] = fleetAppointer(o, f, dc, collector)
	}
	return func(name string) (server.FleetDeps, error) {
		f, err := fleet.Select(fleets, name)
		if err != nil {
			return server.FleetDeps{}, err
		}
		fd := server.FleetDeps{
			BoardDir:  f.BoardPath,
			DocsRoots: f.DocsPaths,
			SetOrchestratorSession: func(id string) error {
				return pinOrchestrator(o.configPath, collector, f.Name, id)
			},
			OrchestratorPreview: wizards[f.Name].Preview,
			Appoint:             wizards[f.Name].Appoint,
		}
		// Left nil without a board, as deps leaves the first fleet's: the route
		// then says this fleet has no board instead of writing somewhere else.
		if f.BoardPath != "" {
			board := f.BoardPath
			fd.CreateCard = func(title, zone string) (string, error) {
				return createCard(board, title, zone, time.Now())
			}
		}
		return fd, nil
	}
}

// fleetAppointer is the orchestrator wizard of one fleet: its board and
// documentation, the configuration file, and that fleet's pin. An existing
// session is vetted first, so another fleet's orchestrator is refused before
// it is handed this fleet's working order.
func fleetAppointer(o runOpts, f fleet.Fleet, dc *daemon.Client, collector *Collector) *orchestrator.Appointer {
	a := appointer(o, config.Config{BoardPath: f.BoardPath, DocsPaths: f.DocsPaths}, dc, collector)
	a.Pin = func(short string) error {
		return pinOrchestrator(o.configPath, collector, f.Name, short)
	}
	a.Vet = func(short string) error {
		return vetPin(collector.Config(), f.Name, short)
	}
	return a
}

// pinOrchestrator pins, or given an empty id unpins, the orchestrator of the
// fleet named name, persisting it before the running collector reports it. A
// session that is another fleet's orchestrator is refused before anything is
// written. The top-level fleet keeps its own key and its own surgical write
// (setOrchestratorSession); a listed fleet's entry is rewritten in place.
func pinOrchestrator(configPath string, collector *Collector, name, id string) error {
	cfg := collector.Config()
	if err := vetPin(cfg, name, id); err != nil {
		return err
	}
	if name == cfg.FleetList()[0].Name {
		return setOrchestratorSession(configPath, collector, id)
	}
	if err := config.SetFleetOrchestrator(configPath, name, id); err != nil {
		return err
	}
	// Found: name was resolved from the fleets the panel started with, which
	// are the collector's.
	collector.SetFleetOrchestrator(name, id)
	return nil
}

// vetPin refuses id as the orchestrator of the fleet named name when the
// configuration would then not load — which, for a configuration that loads
// now, means id already leads another fleet. It wraps
// server.ErrOrchestratorTaken, which the pin route answers with 409.
func vetPin(cfg config.Config, name, id string) error {
	next := cfg
	next.Fleets = slices.Clone(cfg.Fleets)
	switch {
	case name == cfg.FleetList()[0].Name:
		next.OrchestratorSession = id
	default:
		for i := range next.Fleets {
			if next.Fleets[i].Name == name {
				next.Fleets[i].Orchestrator = id
			}
		}
	}
	if err := config.ValidateFleets(next); err != nil {
		return fmt.Errorf("%w: %v", server.ErrOrchestratorTaken, err)
	}
	return nil
}
