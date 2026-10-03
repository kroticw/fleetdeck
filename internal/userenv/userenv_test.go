package userenv

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The login shell's PATH comes first, and what the panel was started with
// that the shell does not name is kept after it.
func TestLoginPATHComesFirstAndKeepsTheRest(t *testing.T) {
	shell := script(t, `PATH=/opt/homebrew/bin:/usr/bin:/bin; export PATH; [ "$1" = -l ] && shift; exec /bin/sh "$@"`)
	got, err := loginPATH(context.Background(), shell, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if want := "/opt/homebrew/bin:/usr/bin:/bin"; got != want {
		t.Fatalf("loginPATH = %q, want %q", got, want)
	}
	if got, want := merge(got, "/usr/bin:/bin:/usr/sbin:/sbin"), "/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"; got != want {
		t.Fatalf("merge = %q, want %q", got, want)
	}
}

// A profile that prints a greeting, or says goodbye in .zlogout, does not end
// up in PATH.
func TestLoginPATHIgnoresWhatTheProfilePrints(t *testing.T) {
	shell := script(t, `echo "Welcome back"; PATH=/a:/b; export PATH; [ "$1" = -l ] && shift; /bin/sh "$@"; echo bye`)
	got, err := loginPATH(context.Background(), shell, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if got != "/a:/b" {
		t.Fatalf("loginPATH = %q, want /a:/b", got)
	}
}

// The shell is asked as a login shell and nothing more: not interactive, no
// terminal on stdin, its own session so nothing it starts can reach for one.
func TestLoginShellIsNotInteractive(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args")
	shell := script(t, `echo "$1" >`+out+`
[ -t 0 ] && echo tty >>`+out+`
[ "$1" = -l ] && shift; exec /bin/sh "$@"`)
	if _, err := loginPATH(context.Background(), shell, os.Environ()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "-l" {
		t.Fatalf("the shell must be run with -l only and no terminal on stdin, got %q", got)
	}
}

// A profile that hangs costs the timeout and no more, and gives no PATH.
func TestLoginPATHGivesUpOnAHangingProfile(t *testing.T) {
	defer func(d time.Duration) { timeout = d }(timeout)
	timeout = 200 * time.Millisecond
	shell := script(t, `sleep 30`)
	start := time.Now()
	if _, err := loginPATH(context.Background(), shell, os.Environ()); err == nil {
		t.Fatal("a profile that never finishes must give an error, not a PATH")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a hanging profile held the panel for %s", took)
	}
}

func TestLoginPATHRefusesAShellThatFails(t *testing.T) {
	if _, err := loginPATH(context.Background(), script(t, `exit 3`), os.Environ()); err == nil {
		t.Fatal("a failing shell must give an error")
	}
	if _, err := loginPATH(context.Background(), filepath.Join(t.TempDir(), "nope"), os.Environ()); err == nil {
		t.Fatal("a missing shell must give an error")
	}
}

// Without an answer from the login shell, the directories package managers
// install into are added after what the panel was started with, when they
// exist.
func TestFallbackAddsKnownDirsThatExist(t *testing.T) {
	there := t.TempDir()
	got := fallback("/usr/bin:/bin", []string{there, filepath.Join(there, "missing"), "/bin"})
	if want := "/usr/bin:/bin:" + there; got != want {
		t.Fatalf("fallback = %q, want %q", got, want)
	}
}

// Start sets the PATH Environ hands out, and this process's own, so a child
// found by name is found on it too.
func TestStartSetsPATH(t *testing.T) {
	t.Setenv("SHELL", script(t, `PATH=/from/login; export PATH; [ "$1" = -l ] && shift; exec /bin/sh "$@"`))
	t.Setenv("PATH", "/usr/bin:/bin")
	Start()
	env := Environ()
	if !contains(env, "PATH=/from/login:/usr/bin:/bin") {
		t.Fatalf("Environ has no login PATH: %v", env)
	}
	if got := os.Getenv("PATH"); got != "/from/login:/usr/bin:/bin" {
		t.Fatalf("process PATH = %q", got)
	}
}

// Before Start, Environ is this process's environment, untouched.
func TestEnvironWithoutStart(t *testing.T) {
	mu.Lock()
	done = nil
	mu.Unlock()
	t.Setenv("PATH", "/only/this")
	if !contains(Environ(), "PATH=/only/this") {
		t.Fatal("Environ without Start must be os.Environ")
	}
}

func contains(env []string, kv string) bool {
	return slices.Contains(env, kv)
}
