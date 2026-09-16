package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// The daemon has no operation that starts a session: `claude --bg` does, and
// prints the new session's short id on a line that opens with "backgrounded".
var (
	backgrounded = regexp.MustCompile(`backgrounded[^\n]*?\b([0-9a-f]{8})\b`)
	ansi         = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
)

// SystemPlaces are where Claude Code's installers put claude outside a home
// directory: Homebrew on Apple silicon and on Intel.
var SystemPlaces = []string{"/opt/homebrew/bin/claude", "/usr/local/bin/claude"}

// ParseShort is the short id `claude --bg` printed, or "" when it printed none.
func ParseShort(out string) string {
	if m := backgrounded.FindStringSubmatch(ansi.ReplaceAllString(out, "")); m != nil {
		return m[1]
	}
	return ""
}

// StartWith is Appointer.Start for the command in argv: `--bg --name <name>`
// appended to it and run in cwd, with no prompt — the session's first message is the
// appointment's own, delivered the way an existing session's is.
//
// A command rather than a path, because an installation is not always reached by
// running a binary: a wrapper that sets up an environment and a configuration
// directory and then execs claude is reached by its own words, and they have to
// arrive before the first argument this function adds.
func StartWith(argv []string) func(ctx context.Context, cwd, name string) (string, error) {
	return func(ctx context.Context, cwd, name string) (string, error) {
		if len(argv) == 0 {
			return "", errors.New("no command to start a session with")
		}
		args := append(append([]string{}, argv[1:]...), "--bg", "--name", name)
		cmd := exec.CommandContext(ctx, argv[0], args...)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		text := strings.TrimSpace(ansi.ReplaceAllString(string(out), ""))
		spoken := strings.Join(argv, " ")
		if err != nil {
			return "", fmt.Errorf("%s --bg: %w: %s", spoken, err, text)
		}
		short := ParseShort(string(out))
		if short == "" {
			return "", fmt.Errorf("%s --bg printed no session id: %s", spoken, text)
		}
		return short, nil
	}
}

// FindClaude is the claude to start sessions with: the one on PATH, else the
// one Claude Code's own installer puts in the home directory, else one of
// system. A window started from the Dock has a PATH with neither Homebrew nor
// ~/.local/bin on it, which is why PATH is not the only place looked at.
func FindClaude(home string, lookPath func(string) (string, error), system []string) (string, error) {
	if p, err := lookPath("claude"); err == nil {
		return p, nil
	}
	places := []string{filepath.Join(home, ".local", "bin", "claude"), filepath.Join(home, ".claude", "local", "claude")}
	for _, p := range append(places, system...) {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("claude was not found on PATH, in ~/.local/bin, in ~/.claude/local, or where Homebrew puts it")
}
