//go:build darwin

package main

import (
	"os"
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// forgetBundle has LaunchServices forget bundle when the test ends. Every app
// bundle a test builds, copies, mounts or starts goes through it: macOS keeps
// every bundle it is shown, under its identifier, long after the directory
// has gone, and on 2026-09-14 the operator's machine held dozens of them --
// any of which could open as "fleetdeck". A bundle under the app's own
// identifier, like a downloaded release, is only safe once forgotten.
func forgetBundle(t *testing.T, bundle string) {
	t.Helper()
	if _, err := os.Stat(supervisor.LsregisterPath); err != nil {
		return
	}
	ls := supervisor.LaunchServices{Lsregister: supervisor.LsregisterPath}
	t.Cleanup(func() {
		if err := ls.Forget(bundle); err != nil {
			t.Errorf("LaunchServices still knows %s: %v", bundle, err)
		}
	})
}

// Forgetting takes a bundle out of the real database, and a test that never
// had LaunchServices see its bundle is not failed for forgetting it.
func TestForgetBundleTakesTheBundleOutOfLaunchServices(t *testing.T) {
	if _, err := os.Stat(supervisor.LsregisterPath); err != nil {
		t.Skipf("no lsregister at %s", supervisor.LsregisterPath)
	}
	ls := supervisor.LaunchServices{Lsregister: supervisor.LsregisterPath}
	app := t.TempDir() + "/" + supervisor.BundleName
	writeStandPlist(t, app)
	t.Run("the case", func(t *testing.T) {
		forgetBundle(t, app)
		if err := ls.Register(app); err != nil {
			t.Fatal(err)
		}
	})
	if in, err := ls.Registered(app); err != nil || in {
		t.Fatalf("after the case ended, Registered = %v, %v; want false", in, err)
	}
}

func writeStandPlist(t *testing.T, app string) {
	t.Helper()
	if err := os.MkdirAll(app+"/Contents/MacOS", 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>stand</string>
<key>CFBundleIdentifier</key><string>` + supervisor.StandBundleID + `</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>`
	if err := os.WriteFile(app+"/Contents/Info.plist", []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
}
