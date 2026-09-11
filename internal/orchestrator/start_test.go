package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What `claude --bg` printed in 2.1.263, as the claude-agents fleet tooling
// parses it: colours, a middle dot, the short id, the name.
const bgOutput = "\x1b[32mbackgrounded\x1b[39m · \x1b[1m0a1b2c3d\x1b[22m · оркестратор (idle — `claude attach 0a1b2c3d`)\n"

func TestParseShortReadsTheBackgroundedLine(t *testing.T) {
	if got := ParseShort(bgOutput); got != "0a1b2c3d" {
		t.Errorf("ParseShort = %q, want 0a1b2c3d", got)
	}
	for _, out := range []string{
		"",
		"Error: not logged in\n",
		// A hex word elsewhere in the output is not the session.
		"deadbeef\nbackgrounded · nothing here\n",
	} {
		if got := ParseShort(out); got != "" {
			t.Errorf("ParseShort(%q) = %q, want nothing", out, got)
		}
	}
}

// fakeClaude writes a claude that records its arguments and directory, then
// prints out and exits with code.
func fakeClaude(t *testing.T, out string, code int) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "claude")
	record = filepath.Join(dir, "record")
	script := "#!/bin/sh\n" +
		"{ pwd; for a in \"$@\"; do echo \"$a\"; done; } > '" + record + "'\n" +
		"printf '%s' '" + strings.ReplaceAll(out, "'", `'\''`) + "'\n" +
		"exit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func TestStartRunsClaudeInTheBackgroundInTheWorkspace(t *testing.T) {
	bin, record := fakeClaude(t, bgOutput, 0)
	cwd := t.TempDir()
	short, err := StartWith(bin)(context.Background(), cwd, "оркестратор")
	if err != nil {
		t.Fatal(err)
	}
	if short != "0a1b2c3d" {
		t.Errorf("short = %q", short)
	}
	got, _ := os.ReadFile(record)
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	realCWD, _ := filepath.EvalSymlinks(cwd)
	if len(lines) == 0 || (lines[0] != cwd && lines[0] != realCWD) {
		t.Errorf("claude ran in %q, want %q", lines[0], cwd)
	}
	if want := []string{"--bg", "--name", "оркестратор"}; strings.Join(lines[1:], " ") != strings.Join(want, " ") {
		t.Errorf("claude was given %q, want %q", lines[1:], want)
	}
}

func TestStartReportsClaudeFailingInItsOwnWords(t *testing.T) {
	bin, _ := fakeClaude(t, "Error: not logged in", 1)
	_, err := StartWith(bin)(context.Background(), t.TempDir(), "orchestrator")
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("err = %v, want claude's own words", err)
	}
}

func TestStartReportsOutputWithNoSession(t *testing.T) {
	bin, _ := fakeClaude(t, "something else entirely", 0)
	_, err := StartWith(bin)(context.Background(), t.TempDir(), "orchestrator")
	if err == nil || !strings.Contains(err.Error(), "something else entirely") {
		t.Errorf("err = %v, want the output it could not read", err)
	}
}

// A window started from the Dock has a PATH without Homebrew or ~/.local/bin,
// so the places Claude Code installs itself are looked at after PATH.
func TestFindClaudeLooksAtPathThenTheInstallPlaces(t *testing.T) {
	home := t.TempDir()
	notFound := func(string) (string, error) { return "", errors.New("not in PATH") }

	if _, err := FindClaude(home, notFound, nil); err == nil {
		t.Error("found a claude where there is none")
	}

	local := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := FindClaude(home, notFound, nil); err != nil || got != local {
		t.Errorf("FindClaude = %q, %v; want %q", got, err, local)
	}

	onPath := func(string) (string, error) { return "/on/path/claude", nil }
	if got, _ := FindClaude(home, onPath, nil); got != "/on/path/claude" {
		t.Errorf("FindClaude with claude on PATH = %q", got)
	}

	extra := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(extra, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, _ := FindClaude(t.TempDir(), notFound, []string{extra}); got != extra {
		t.Errorf("FindClaude with an extra place = %q, want %q", got, extra)
	}
}
