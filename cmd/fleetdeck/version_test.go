package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildTimeout bounds the two subprocesses below so a wedged toolchain fails the test
// instead of sitting until the whole package's test deadline runs out.
const buildTimeout = 4 * time.Minute

// TestReleaseBinaryReportsTheInjectedVersion builds the release binary the way a
// release is built and asks it what version it is.
//
// internal/version's own tests assign `value` directly and know nothing of the
// Makefile; `make verify-ldflags` proves the -X symbol path lands in a *test* binary.
// Neither of them runs the thing that actually ships. The linker accepts an -X target
// that names a symbol which does not exist without complaining, so a module rename, a
// package move or a renamed variable would leave every release binary reporting "dev"
// with nothing anywhere saying so — and until this task there was no binary to catch
// it with.
func TestReleaseBinaryReportsTheInjectedVersion(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH, so the release build cannot be exercised here")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	const want = "1.2.3-release-check"
	binDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	build := exec.CommandContext(ctx, "make", "build", "VERSION="+want, "BINDIR="+binDir)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("make build: %v\n%s", err, out)
	}

	out, err := exec.CommandContext(ctx, filepath.Join(binDir, "fleetdeck"), "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("fleetdeck --version: %v\n%s", err, out)
	}

	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("the release binary must report the version it was built with: want %q, got %q", want, got)
	}
}
