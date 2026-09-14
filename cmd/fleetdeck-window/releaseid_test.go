//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// A bundle under the stand identifier is never one LaunchServices opens as the
// app, and for the same reason must never be published as the app: an update
// to it would land beside the installed app under another name. The release
// gate asks the app it checks for the identifier it was told, and a signed
// release is told the app's own or it stops.

// appIdentifierCheck runs the gate's identifier question from
// scripts/dist-app-checks.sh on a bundle whose plist names plistID, told
// wantID and seal, the way the gates source that file.
func appIdentifierCheck(t *testing.T, plistID, wantID, seal string) (string, error) {
	t.Helper()
	app := filepath.Join(t.TempDir(), supervisor.BundleName)
	if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>` + plistID + `</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
work=$(mktemp -d)
src=cmd/fleetdeck-window
fail() { echo "gate: $*" >&2; exit 1; }
. scripts/dist-app-checks.sh
app_carries_the_identifier "$1" "$2" "$3"
echo ok`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", app, wantID, seal)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTheReleaseGatePassesTheIdentifierItWasTold(t *testing.T) {
	for _, c := range []struct{ id, seal string }{
		{"dev.fleetdeck.window", "notarized"},
		{"dev.fleetdeck.window", "developer-id"},
		{"dev.fleetdeck.window", "adhoc"},
		{supervisor.StandBundleID, "adhoc"},
	} {
		if out, err := appIdentifierCheck(t, c.id, c.id, c.seal); err != nil || !strings.Contains(out, "ok") {
			t.Errorf("%s, %s: %v\n%s", c.id, c.seal, err, out)
		}
	}
}

func TestTheReleaseGateRefusesAnIdentifierOtherThanItWasTold(t *testing.T) {
	out, err := appIdentifierCheck(t, supervisor.StandBundleID, "dev.fleetdeck.window", "adhoc")
	if err == nil || !strings.Contains(out, supervisor.StandBundleID) {
		t.Fatalf("a bundle under %s passed as dev.fleetdeck.window: %v\n%s", supervisor.StandBundleID, err, out)
	}
}

// Signed is a release; a release is the app. A stand build told to expect its
// own identifier still stops there.
func TestTheReleaseGateRefusesASignedAppUnderAnyIdentifierButTheApps(t *testing.T) {
	for _, seal := range []string{"developer-id", "notarized"} {
		out, err := appIdentifierCheck(t, supervisor.StandBundleID, supervisor.StandBundleID, seal)
		if err == nil || !strings.Contains(out, "dev.fleetdeck.window") {
			t.Fatalf("a %s app under %s passed the gate: %v\n%s", seal, supervisor.StandBundleID, err, out)
		}
	}
}

// The workflow names the identifier on every command that builds or checks
// the app, on the command line, where neither the environment nor a default
// can change it.
func TestTheReleaseWorkflowBuildsAndChecksTheAppUnderItsOwnIdentifier(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"dist-app", "verify-dist-app", "dist-dmg", "verify-dist-dmg"} {
		line := regexp.MustCompile(`(?m)^\s*run: make ` + target + ` .*$`).FindString(string(data))
		if line == "" {
			t.Errorf("release.yaml runs no make %s", target)
			continue
		}
		if !strings.Contains(line, "BUNDLE_ID=dev.fleetdeck.window") {
			t.Errorf("release.yaml runs %q without BUNDLE_ID=dev.fleetdeck.window", strings.TrimSpace(line))
		}
	}
}
