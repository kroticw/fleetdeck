package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/userenv"
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
		Paths:     orchestrator.Paths{Board: cfg.BoardPath, Docs: cfg.DocsPaths, Config: o.configPath},
		List:      dc.ListSessions,
		Send:      dc.SendText,
		SendFirst: dc.SendFirst,
		Start:     sessionStarter(o, cfg.Agent),
		Pin: func(short string) error {
			return setOrchestratorSession(o.configPath, collector, short)
		},
	}
}

// fleetDispatcher hands one fleet's cards to sessions of their own: started as
// workers (workerStarter), in the checkout each card's repo field names under
// the home directory, and written into the card with the same commit the panel
// makes for a field set by hand. workers is the configuration's workers section
// as it is when a worker starts.
func fleetDispatcher(o runOpts, cfg config.Config, dc *daemon.Client, workers func() config.Workers) *orchestrator.Dispatcher {
	home, _ := os.UserHomeDir()
	return &orchestrator.Dispatcher{
		Start:     workerStarter(o, cfg.Agent, workers),
		List:      dc.ListSessions,
		Send:      dc.SendText,
		SendFirst: dc.SendFirst,
		SetField:  setCardField,
		Home:      home,
	}
}

// workerLaunch is the workers section as the flags a worker is started with;
// what it leaves unset is orchestrator.Launch's default.
func workerLaunch(w config.Workers) orchestrator.Launch {
	return orchestrator.Launch{Model: w.Model, PermissionMode: w.PermissionMode, Sandbox: w.Sandbox}
}

// workerStarter is sessionStarter for a worker session: the same claude, with
// the workers section's flags added (orchestrator.Launch), read from workers
// each time a worker starts so that an edited configuration applies to the
// next worker. Nil where sessionStarter is nil.
func workerStarter(o runOpts, agent config.AgentConfig, workers func() config.Workers) func(ctx context.Context, cwd, name string) (string, error) {
	if sessionStarter(o, agent) == nil {
		return nil
	}
	return func(ctx context.Context, cwd, name string) (string, error) {
		return sessionStarter(o, agent, workerLaunch(workers()).Args()...)(ctx, cwd, name)
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
// A configured command (agent.command) is run as written — it is how an installation
// other than the default one is reached, and looking for a claude of our own instead
// would start sessions in the wrong fleet. With no command configured, a panel looks
// claude up each time it starts one, so a claude installed while the panel runs is
// found without a restart, and runs it in the installation the panel reads (claudeEnv).
//
// extra are flags for the session itself, after the command and before the
// `--bg --name` StartWith adds: a worker's launch flags (workerStarter).
func sessionStarter(o runOpts, agent config.AgentConfig, extra ...string) func(ctx context.Context, cwd, name string) (string, error) {
	if o.standSocket != "" {
		if o.standClaude == "" {
			return nil
		}
		return orchestrator.StartWith(slices.Concat([]string{o.standClaude}, extra))
	}
	if len(agent.Command) > 0 {
		return orchestrator.StartWith(slices.Concat(agent.Command, extra))
	}
	return func(ctx context.Context, cwd, name string) (string, error) {
		// First: it waits for the operator's PATH, which claude is then looked
		// up on and run with.
		environ := userenv.Environ()
		home, _ := os.UserHomeDir()
		bin, err := orchestrator.FindClaude(home, exec.LookPath, claudePlaces)
		if err != nil {
			return "", err
		}
		return orchestrator.StartWithEnv(slices.Concat([]string{bin}, extra), claudeEnv(environ, agent.ConfigDir))(ctx, cwd, name)
	}
}

// claudeEnv is the environment a claude found by FindClaude runs with: environ with
// CLAUDE_CONFIG_DIR set to configDir, or with no CLAUDE_CONFIG_DIR at all when
// configDir is empty. Claude Code picks its installation by that one variable, and the
// panel reads the installation the configuration names (claudeDirOf) — so the child
// gets it from the configuration, never from however the panel happened to be launched:
// a terminal that exported the variable would otherwise start sessions in one
// installation while the panel, opened from the Dock, shows another. A configured
// command is not given this: it is a wrapper that picks its installation itself.
func claudeEnv(environ []string, configDir string) []string {
	env := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=")
	})
	if configDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	return env
}

// stopWait bounds one stop. It is a command that asks the daemon and returns;
// a stop that has not answered in ten seconds is not going to.
const stopWait = 10 * time.Second

// sessionStopper is how this panel stops a session, or nil when it must not:
// `stop <short>` run by the same claude sessionStarter starts sessions with,
// found the same way and run in the same installation. The session leaves the
// list of running ones and stays resumable, with its history.
//
// A stand given no claude of its own stops nothing, for the reason it starts
// nothing: the claude on PATH reaches the operator's real daemon, and a
// stand's done card would put out a session of the operator's own.
func sessionStopper(o runOpts, agent config.AgentConfig) func(ctx context.Context, short string) error {
	// Nil is a claude looked up at each stop, as sessionStarter looks one up
	// at each start.
	var fixed []string
	switch {
	case o.standSocket != "":
		if o.standClaude == "" {
			return nil
		}
		fixed = []string{o.standClaude}
	case len(agent.Command) > 0:
		fixed = agent.Command
	}
	return func(ctx context.Context, short string) error {
		argv, env := fixed, []string(nil)
		if argv == nil {
			environ := userenv.Environ()
			home, _ := os.UserHomeDir()
			bin, err := orchestrator.FindClaude(home, exec.LookPath, claudePlaces)
			if err != nil {
				return err
			}
			argv = []string{bin}
			env = claudeEnv(environ, agent.ConfigDir)
		}
		ctx, cancel := context.WithTimeout(ctx, stopWait)
		defer cancel()
		args := slices.Concat(argv[1:], []string{"stop", short})
		cmd := exec.CommandContext(ctx, argv[0], args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("stop session %s: %w: %s", short, err, strings.TrimSpace(string(out)))
		}
		return nil
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
