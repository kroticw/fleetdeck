package main

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// claudePlaces are where a claude is looked for outside PATH and the home
// directory. A variable so a test can empty it: on a developer's machine these
// hold the real claude, and a real `claude --bg` is a real session.
var claudePlaces = orchestrator.SystemPlaces

// appointer is the orchestrator wizard's back end: the panel's own board,
// documentation and configuration, its daemon client, and the same pin the
// orchestrator column's picker writes.
func appointer(o runOpts, cfg config.Config, dc *daemon.Client, collector *Collector) *orchestrator.Appointer {
	return &orchestrator.Appointer{
		Paths: orchestrator.Paths{Board: cfg.BoardPath, Docs: cfg.DocsPaths, Config: o.configPath},
		List:  dc.ListSessions,
		Send:  dc.SendText,
		Start: sessionStarter(o),
		Pin: func(short string) error {
			return setOrchestratorSession(o.configPath, collector, short)
		},
	}
}

// sessionStarter is how this panel starts a session, or nil when it must not.
//
// A stand (-stand-socket) starts sessions only with the claude it was given in
// -stand-claude, and with none when it was given none. It never looks for
// one: `claude --bg` reaches the operator's real daemon whatever socket the
// panel reads, so a stand that found the real claude would start real
// sessions in the operator's fleet — the thing -stand-socket exists to rule
// out. As there, the guarantee is a path that does not exist, not a check.
//
// A panel looks claude up each time it starts one, so a claude installed
// while the panel runs is found without a restart.
func sessionStarter(o runOpts) func(ctx context.Context, cwd, name string) (string, error) {
	if o.standSocket != "" {
		if o.standClaude == "" {
			return nil
		}
		return orchestrator.StartWith(o.standClaude)
	}
	return func(ctx context.Context, cwd, name string) (string, error) {
		home, _ := os.UserHomeDir()
		bin, err := orchestrator.FindClaude(home, exec.LookPath, claudePlaces)
		if err != nil {
			return "", err
		}
		return orchestrator.StartWith(bin)(ctx, cwd, name)
	}
}

// checkStandClaude refuses -stand-claude where it would mean nothing or
// nothing safe: without -stand-socket it would be ignored while the panel
// used whatever claude it found, and given empty it reads as "no claude"
// while looking like a configured one.
func checkStandClaude(socketGiven, claudeGiven bool, claude string) error {
	switch {
	case claudeGiven && !socketGiven:
		return errors.New("-stand-claude is for a stand: give it with -stand-socket, or leave it out")
	case claudeGiven && claude == "":
		return errors.New("-stand-claude was given empty; leave it out for a stand that starts no sessions")
	}
	return nil
}
