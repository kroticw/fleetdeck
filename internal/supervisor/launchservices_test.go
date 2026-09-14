//go:build darwin

package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// StandBundleID is what every bundle a test or a stand makes is called, never
// the app's own: LaunchServices keeps every bundle it is shown under its
// identifier, and on 2026-09-14 it opened a stand's leftover as the operator's
// app. The test here goes through the real LaunchServices, on a bundle of its
// own, and takes it back out.
func TestABundleForgottenIsNoLongerInLaunchServices(t *testing.T) {
	if _, err := os.Stat(LsregisterPath); err != nil {
		t.Skipf("no lsregister at %s", LsregisterPath)
	}
	app := standBundle(t, filepath.Join(t.TempDir(), BundleName))
	ls := LaunchServices{Lsregister: LsregisterPath}
	t.Cleanup(func() { _ = ls.Forget(app) })

	if err := ls.Register(app); err != nil {
		t.Fatal(err)
	}
	if in, err := ls.Registered(app); err != nil || !in {
		t.Fatalf("Registered after Register = %v, %v; want true", in, err)
	}
	if err := ls.Forget(app); err != nil {
		t.Fatal(err)
	}
	if in, err := ls.Registered(app); err != nil || in {
		t.Fatalf("Registered after Forget = %v, %v; want false", in, err)
	}
}

// Forgetting a bundle LaunchServices never saw -- one a test built and never
// opened -- leaves the database as it wants it, and is not a failure.
// lsregister -u itself exits 1 on such a bundle, with -10814
// (kLSApplicationNotFoundErr): measured the first time a test built a bundle
// and forgot it.
func TestForgettingABundleNeverRegisteredIsNotAnError(t *testing.T) {
	if _, err := os.Stat(LsregisterPath); err != nil {
		t.Skipf("no lsregister at %s", LsregisterPath)
	}
	app := standBundle(t, filepath.Join(t.TempDir(), BundleName))
	if err := (LaunchServices{Lsregister: LsregisterPath}).Forget(app); err != nil {
		t.Fatalf("Forget of a bundle never registered: %v", err)
	}
}

// A path LaunchServices never had is not reported as registered because
// another path begins with it.
func TestRegisteredMatchesTheWholePath(t *testing.T) {
	if _, err := os.Stat(LsregisterPath); err != nil {
		t.Skipf("no lsregister at %s", LsregisterPath)
	}
	dir := t.TempDir()
	app := standBundle(t, filepath.Join(dir, BundleName))
	ls := LaunchServices{Lsregister: LsregisterPath}
	t.Cleanup(func() { _ = ls.Forget(app) })
	if err := ls.Register(app); err != nil {
		t.Fatal(err)
	}
	if in, err := ls.Registered(filepath.Join(dir, "fleet")); err != nil || in {
		t.Fatalf("Registered of a prefix = %v, %v; want false", in, err)
	}
}

// standBundle makes an app bundle at app under the stand identifier: an
// Info.plist and /usr/bin/true as its executable.
func standBundle(t *testing.T, app string) string {
	t.Helper()
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/cp", "/usr/bin/true", filepath.Join(macos, "stand")).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>stand</string>
<key>CFBundleIdentifier</key><string>` + StandBundleID + `</string>
<key>CFBundleName</key><string>stand</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	return app
}
