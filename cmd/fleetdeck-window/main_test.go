//go:build darwin

package main

import (
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultURLMatchesTheStatusReporterConvention(t *testing.T) {
	// cmd/fleetdeck-status defaults to the same address when FLEETDECK_ENDPOINT
	// is unset. Two different defaults for "where the panel lives" would be a
	// silent way for one of them to go stale.
	if defaultURL != "http://127.0.0.1:7777/" {
		t.Fatalf("defaultURL = %q, want http://127.0.0.1:7777/", defaultURL)
	}
}

// --- Mach-O verification: the real defect this task guards against -------
//
// webview_go has no non-cgo fallback at all (its only non-test source file
// has an unconditional `import "C"`), so CGO_ENABLED=0 or an unconfigured
// cross-GOARCH build already fails loudly at compile time -- verified by
// hand while choosing this dependency, not assumed. The defect this guards
// against instead is the one that *would* compile and *would* produce a
// binary: a future cross-compilation setup (an explicit CC, CGO_CFLAGS with
// an -arch flag) that targets the wrong architecture or misses a framework.
// That failure is silent at build time and only shows up when someone tries
// to run the binary -- exactly the "quiet failure disguised as success"
// class this project has spent this session catching in other packages.
//
// hasWebKitDylib is a thin wrapper around debug/macho, part of the Go
// standard library -- no external tool (otool, lipo) has to exist on
// whatever runs this test, so the check itself can never silently skip for
// a missing tool.
func hasWebKitDylib(path string) (bool, error) {
	f, err := macho.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	for _, l := range f.Loads {
		dylib, ok := l.(*macho.Dylib)
		if !ok {
			continue
		}
		if strings.Contains(dylib.Name, "WebKit.framework") {
			return true, nil
		}
	}
	return false, nil
}

func buildBinary(t *testing.T, dir, pkgDir string) string {
	t.Helper()
	out := filepath.Join(dir, filepath.Base(pkgDir)+"-bin")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = pkgDir
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build in %s: %v\n%s", pkgDir, err, output)
	}
	return out
}

// TestWindowBinaryLinksAgainstWebKit is the loud-failure guarantee itself:
// the actual cmd/fleetdeck-window binary, built the same way any build of
// this repository builds it, must really be linked against WebKit.framework
// -- not merely present as a file on disk.
func TestWindowBinaryLinksAgainstWebKit(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("WebKit.framework is a macOS concept; this only means something on darwin")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	bin := buildBinary(t, t.TempDir(), wd)
	ok, err := hasWebKitDylib(bin)
	if err != nil {
		t.Fatalf("reading %s as Mach-O: %v", bin, err)
	}
	if !ok {
		t.Fatal("cmd/fleetdeck-window's own binary is not linked against WebKit.framework -- cgo did not really engage")
	}
}

// TestHasWebKitDylibSaysNoOnAPlainBinary is the mutation self-check the task
// asked for: hasWebKitDylib is itself untested code until something proves
// it can say "no". Without this, TestWindowBinaryLinksAgainstWebKit passing
// would be indistinguishable from a checker that always returns true.
func TestHasWebKitDylibSaysNoOnAPlainBinary(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the comparison only means something against darwin Mach-O binaries")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module plaincheck\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := buildBinary(t, dir, dir)
	ok, err := hasWebKitDylib(bin)
	if err != nil {
		t.Fatalf("reading %s as Mach-O: %v", bin, err)
	}
	if ok {
		t.Fatal("a plain, non-cgo binary must not report a WebKit link -- the checker would always say yes")
	}
}
