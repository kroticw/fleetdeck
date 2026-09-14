package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/ci-window-stand.sh captures the screen to show what the window it
// started looked like. On a CI runner the screen holds the stand and nothing
// else; on a person's Mac a capture of the screen is everything they have open.
// So the whole screen is captured on GitHub Actions and nowhere else.

// standCapture runs capture_screen from scripts/stand-capture.sh, the way the
// stand sources it, with screencapture stubbed and GITHUB_ACTIONS set to
// githubActions (unset when empty). It reports whether screencapture was run.
func standCapture(t *testing.T, githubActions string) (bool, string) {
	t.Helper()
	stubs := t.TempDir()
	marker := filepath.Join(stubs, "captured")
	if err := os.WriteFile(filepath.Join(stubs, "screencapture"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
. scripts/stand-capture.sh
capture_screen "$1"`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", filepath.Join(stubs, "window.png"))
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = []string{"PATH=" + stubs + ":/usr/bin:/bin"}
	if githubActions != "" {
		cmd.Env = append(cmd.Env, "GITHUB_ACTIONS="+githubActions)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("capture_screen with GITHUB_ACTIONS=%q: %v\n%s", githubActions, err, out)
	}
	_, statErr := os.Stat(marker)
	return statErr == nil, string(out)
}

func TestTheWindowStandCapturesTheScreenOnlyOnGitHubActions(t *testing.T) {
	if captured, out := standCapture(t, "true"); !captured {
		t.Fatalf("on GitHub Actions the stand took no screenshot:\n%s", out)
	}
	for _, env := range []string{"", "false"} {
		captured, out := standCapture(t, env)
		if captured {
			t.Fatalf("with GITHUB_ACTIONS=%q the stand captured the whole screen", env)
		}
		if !strings.Contains(out, "screenshot skipped") {
			t.Fatalf("with GITHUB_ACTIONS=%q the stand did not say it skipped the screenshot:\n%s", env, out)
		}
	}
}

func TestTheWindowStandCapturesTheScreenOnlyThroughTheGuard(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "ci-window-stand.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		if strings.Contains(code, "screencapture") {
			t.Fatalf("scripts/ci-window-stand.sh runs screencapture itself: %q; capture through capture_screen from scripts/stand-capture.sh", code)
		}
	}
	if !strings.Contains(string(raw), "capture_screen ") {
		t.Fatal("scripts/ci-window-stand.sh no longer captures through capture_screen")
	}
}
