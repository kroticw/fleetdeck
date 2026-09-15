// Package darwinsdkroot holds the test of scripts/darwin-sdkroot.sh, the
// Makefile's choice of the macOS SDK every cgo build on darwin links against.
// It is a package of its own, with no cgo, so the test runs wherever there is
// a /bin/sh, not only where the window links.
package darwinsdkroot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The script is run with an xcrun of the test's own first on PATH and no
// /usr/bin on it at all, so neither make nor any of xcrun's shims runs under
// the made-up paths below: under an SDKROOT that is no SDK, a shim asks macOS
// to install the Command Line Tools, on the screen of whoever runs the tests.

func script(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "darwin-sdkroot.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeXcrun is a directory holding an xcrun that writes body, and the file it
// records each call's arguments in.
func fakeXcrun(t *testing.T, body string) (dir, calls string) {
	t.Helper()
	dir = t.TempDir()
	calls = filepath.Join(dir, "calls")
	stub := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n" + body
	if err := os.WriteFile(filepath.Join(dir, "xcrun"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, calls
}

func run(t *testing.T, dir, sdkroot string) (out, stderr string, err error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", script(t))
	cmd.Env = []string{"PATH=" + dir + ":/bin", "SDKROOT=" + sdkroot}
	var errOut strings.Builder
	cmd.Stderr = &errOut
	stdout, err := cmd.Output()
	return strings.TrimSpace(string(stdout)), errOut.String(), err
}

func TestAGivenSDKROOTIsUsedAndXcrunIsNotAsked(t *testing.T) {
	dir, calls := fakeXcrun(t, "echo /Selected/MacOSX.sdk\n")
	if got, _, err := run(t, dir, "/Given/MacOSX.sdk"); err != nil || got != "/Given/MacOSX.sdk" {
		t.Fatalf("with SDKROOT given: %q, %v; want the given SDK", got, err)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("with SDKROOT given, xcrun was asked anyway")
	}
}

func TestWithNoSDKROOTTheSelectedDeveloperDirectorysSDKIsUsed(t *testing.T) {
	dir, calls := fakeXcrun(t, "echo /Selected/MacOSX.sdk\n")
	if got, _, err := run(t, dir, ""); err != nil || got != "/Selected/MacOSX.sdk" {
		t.Fatalf("with no SDKROOT: %q, %v; want xcrun's answer", got, err)
	}
	if data, _ := os.ReadFile(calls); strings.TrimSpace(string(data)) != "--sdk macosx --show-sdk-path" {
		t.Fatalf("xcrun was asked %q, want the selected developer directory's SDK", data)
	}
}

func TestAnXcrunThatNamesNoSDKFailsSayingToSetSDKROOT(t *testing.T) {
	dir, _ := fakeXcrun(t, "")
	if got, stderr, err := run(t, dir, ""); err == nil || got != "" || !strings.Contains(stderr, "SDKROOT") {
		t.Fatalf("with xcrun answering nothing: %q, %v, %q; want a failure that says to set SDKROOT", got, err, stderr)
	}
}

// An Xcode whose license is not accepted: xcrun exits 69 and says why, and the
// script says both rather than naming no SDK in silence.
func TestAnXcrunRefusingForItsLicenseFailsSayingWhy(t *testing.T) {
	dir, _ := fakeXcrun(t, "echo 'You have not agreed to the Xcode license agreements.' >&2\nexit 69\n")
	got, stderr, err := run(t, dir, "")
	if err == nil || got != "" || !strings.Contains(stderr, "exit 69") || !strings.Contains(stderr, "license") {
		t.Fatalf("with xcrun refusing for its license: %q, %v, %q; want a failure naming exit 69 and what xcrun said", got, err, stderr)
	}
}
