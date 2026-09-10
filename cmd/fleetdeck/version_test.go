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

	// Both ways of asking are exercised against the same binary, and their output is
	// compared byte for byte rather than after trimming: the flag and the subcommand
	// are two entry points to one answer, and a release is verified through whichever
	// one the verification script happens to call. If they can disagree, verifying one
	// of them says nothing about the other.
	outputs := map[string]string{}
	for _, args := range [][]string{{"--version"}, {"version"}} {
		out, err := exec.CommandContext(ctx, filepath.Join(binDir, "fleetdeck"), args...).CombinedOutput()
		if err != nil {
			t.Fatalf("fleetdeck %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Fatalf("the release binary must report the version it was built with: `fleetdeck %s` printed %q, want %q",
				strings.Join(args, " "), got, want)
		}
		outputs[strings.Join(args, " ")] = string(out)
	}

	if outputs["--version"] != outputs["version"] {
		t.Fatalf("the flag and the subcommand must print identical output: --version printed %q, version printed %q",
			outputs["--version"], outputs["version"])
	}
}

// TestRunVersionPrintsOneNonEmptyLine pins the shape of the output, not its value:
// whatever the build was stamped with, `fleetdeck version` must answer with exactly
// one non-empty line, because that output is what a human reads out of a bug report
// and what the release verification compares against the tag.
func TestRunVersionPrintsOneNonEmptyLine(t *testing.T) {
	var out strings.Builder
	runVersion(&out)

	got := out.String()
	if strings.TrimSpace(got) == "" {
		t.Fatal("version output must never be empty: an unversioned binary cannot be supported")
	}
	if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("version must be exactly one terminated line, got %q", got)
	}
}
