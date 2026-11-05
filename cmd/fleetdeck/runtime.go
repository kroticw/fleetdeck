package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/codex"
	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/executor"
	"github.com/kroticw/fleetdeck/internal/userenv"
)

// runtimeCommand is `fleetdeck runtime --key K --state-dir D`: the process
// that owns one Codex session apart from any panel or window. Everything else
// comes from the record the panel saved; the arguments carry no secrets or
// task text, and it never opens the panel's HTTP port.
func runtimeCommand(args []string) error {
	fs := flag.NewFlagSet("runtime", flag.ContinueOnError)
	key := fs.String("key", "", "runtime key codex:<uuid>")
	stateDir := fs.String("state-dir", "", "private runtime state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *key == "" || *stateDir == "" || fs.NArg() != 0 {
		return errors.New("usage: fleetdeck runtime --key codex:<uuid> --state-dir <directory>")
	}
	store, err := codexruntime.OpenStore(*stateDir)
	if err != nil {
		return err
	}
	record, err := store.Read(*key)
	_ = store.Close()
	if err != nil {
		return err
	}
	path := record.ConfigPath
	if path == "" {
		path = config.DefaultPath()
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	userenv.Start()
	spec, err := codexService(cfg.Codex, *stateDir, executor.Key(*key), userenv.Environ())
	if err != nil {
		return err
	}
	// Detached from any terminal; a hangup is not a reason to stop.
	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	return codexruntime.Serve(ctx, spec)
}

// codexHome is the CODEX_HOME the runtimes run under: the configured one,
// else the environment's, else Codex's own default.
func codexHome(c config.CodexConfig, environ []string) (string, error) {
	if c.Home != "" {
		return c.Home, nil
	}
	if home := envValue(environ, "CODEX_HOME"); home != "" {
		return home, nil
	}
	user, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(user, ".codex"), nil
}

// codexService maps the Codex settings onto one runtime. The same CODEX_HOME
// serves the daemon, its control socket and the TUI.
func codexService(c config.CodexConfig, stateDir string, key executor.Key, environ []string) (codexruntime.ServiceSpec, error) {
	if !c.Enabled {
		return codexruntime.ServiceSpec{}, executor.ErrUnavailable
	}
	if err := c.Validate(); err != nil {
		return codexruntime.ServiceSpec{}, err
	}
	home, err := codexHome(c, environ)
	if err != nil {
		return codexruntime.ServiceSpec{}, err
	}
	env := append(slices.DeleteFunc(slices.Clone(environ), func(e string) bool { return strings.HasPrefix(e, "CODEX_HOME=") }), "CODEX_HOME="+home)
	executable := ""
	if len(c.Command) == 0 {
		found, err := exec.LookPath("codex")
		if err != nil {
			return codexruntime.ServiceSpec{}, fmt.Errorf("find codex: %w", err)
		}
		if executable, err = filepath.Abs(found); err != nil {
			return codexruntime.ServiceSpec{}, err
		}
	}
	thread := map[string]any{}
	for name, value := range map[string]string{"model": c.Model, "sandbox": c.Sandbox, "approvalPolicy": c.ApprovalPolicy} {
		if value != "" {
			thread[name] = value
		}
	}
	if c.Effort != "" {
		// Effort is per turn in the app-server; on the thread it is the
		// config every turn starts from.
		thread["config"] = map[string]any{"model_reasoning_effort": c.Effort}
	}
	return codexruntime.ServiceSpec{
		StateDir: stateDir, Key: key, Cols: 120, Rows: 40, Thread: thread,
		NativeSocket: filepath.Join(home, "app-server-control", "app-server-control.sock"),
		EnsureNative: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			argv := codexDaemonArgv(c, executable)
			cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
			cmd.Env = env
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("codex app-server daemon start: %w", err)
			}
			return nil
		},
		Launch: func(threadID string) ([]string, []string, error) {
			launch, err := codex.BuildLaunch(c, executable, threadID, env)
			return launch.Argv, launch.Env, err
		},
	}, nil
}

// codexDaemonArgv starts Codex's own local daemon; the command is idempotent.
func codexDaemonArgv(c config.CodexConfig, executable string) []string {
	base := slices.Clone(c.Command)
	if len(base) == 0 {
		base = []string{executable}
	}
	return append(base, "app-server", "daemon", "start")
}

func envValue(environ []string, key string) string {
	for _, entry := range slices.Backward(environ) {
		if v, ok := strings.CutPrefix(entry, key+"="); ok {
			return v
		}
	}
	return ""
}
