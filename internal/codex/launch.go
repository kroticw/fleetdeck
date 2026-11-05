// Package codex integrates Codex sessions with Fleetdeck's executor contract.
package codex

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/executor"
)

type Launch struct{ Argv, Env []string }

// BuildLaunch resumes an explicitly registered thread in the ordinary local
// TUI. executable is the absolute binary found by the application's PATH
// resolver; a configured wrapper takes precedence. It starts no process and
// creates no CODEX_HOME or authentication files.
func BuildLaunch(c config.CodexConfig, executable, threadID string, environ []string) (Launch, error) {
	if !c.Enabled {
		return Launch{}, executor.ErrUnavailable
	}
	if err := c.Validate(); err != nil {
		return Launch{}, err
	}
	if _, _, err := executor.ParseKey("codex:" + threadID); err != nil {
		return Launch{}, errors.New("codex launch requires a registered native thread UUID")
	}
	argv := append([]string(nil), c.Command...)
	if len(argv) == 0 {
		if !filepath.IsAbs(executable) {
			return Launch{}, errors.New("codex executable must be an absolute path")
		}
		argv = []string{executable}
	}
	for _, pair := range [][2]string{{"--profile", c.Profile}, {"--model", c.Model}, {"--sandbox", c.Sandbox}, {"--ask-for-approval", c.ApprovalPolicy}} {
		if pair[1] != "" {
			argv = append(argv, pair[0], pair[1])
		}
	}
	argv = append(argv, "resume", threadID)
	env := codexruntime.TerminalEnvironment(environ)
	if c.Home != "" {
		out := make([]string, 0, len(env)+1)
		for _, entry := range env {
			if !strings.HasPrefix(entry, "CODEX_HOME=") {
				out = append(out, entry)
			}
		}
		env = append(out, "CODEX_HOME="+c.Home)
	}
	return Launch{Argv: argv, Env: env}, nil
}
