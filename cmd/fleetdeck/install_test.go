package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallWritesBothBinariesWithAVerifiableHash runs the real `make install`
// and interrogates what it wrote, the same discipline dist_test.go and
// version_test.go already hold this project to for every other release path:
// a target's own printed "done" is not proof, and this is the one target
// whose whole reason for existing is to replace a binary something outside
// this repository already trusts (spec: the operator's launch agent, Claude
// Code's statusLine.command).
func TestInstallWritesBothBinariesWithAVerifiableHash(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH, so install cannot be exercised here")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	installDir := t.TempDir()
	const want = "1.2.3-install-check"

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "make", "install", "VERSION="+want, "INSTALLDIR="+installDir)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make install: %v\n%s", err, out)
	}

	for _, name := range []string{"fleetdeck", "fleetdeck-status"} {
		path := filepath.Join(installDir, name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("install did not write %s: %v", path, err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("%s is not executable: mode=%v err=%v", path, info.Mode(), err)
		}

		sum := sha256.Sum256(body)
		wantLine := "install: " + path + "  sha256=" + hex.EncodeToString(sum[:])
		if !strings.Contains(string(out), wantLine) {
			t.Fatalf("install's own output does not name %s with its actual hash;\nwant line: %q\ngot output:\n%s", name, wantLine, out)
		}
	}

	// fleetdeck: reuse the same "ask the binary what it is" proof
	// version_test.go already applies to `make build`'s own output --
	// install must stamp the same LDFLAGS path, not a different build.
	versionOut, err := exec.CommandContext(ctx, filepath.Join(installDir, "fleetdeck"), "version").CombinedOutput()
	if err != nil {
		t.Fatalf("fleetdeck version: %v\n%s", err, versionOut)
	}
	if got := strings.TrimSpace(string(versionOut)); got != want {
		t.Fatalf("installed fleetdeck reports %q, want the version install was run with (%q)", got, want)
	}

	// fleetdeck-status: a real, working binary, not a placeholder -- fed a
	// minimal statusline payload, it must answer with its own render rather
	// than exit with an error or print nothing.
	statusCmd := exec.CommandContext(ctx, filepath.Join(installDir, "fleetdeck-status"))
	statusCmd.Stdin = strings.NewReader(`{"model":{"display_name":"Test"},"cost":{"total_cost_usd":1},"context_window":{"used_percentage":5}}`)
	statusCmd.Env = append(os.Environ(), "FLEETDECK_ENDPOINT=http://127.0.0.1:1")
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fleetdeck-status: %v\n%s", err, statusOut)
	}
	if !strings.Contains(string(statusOut), "Test") {
		t.Fatalf("installed fleetdeck-status did not render the sample input: %q", statusOut)
	}
}

// TestInstallOverwritesWhateverWasThereBefore is the case install exists
// for: a stale binary already sitting at the target path, exactly the
// shape this project found ~/.local/bin/fleetdeck-status in. A target that
// silently skipped an existing file, or appended instead of replacing,
// would reproduce that staleness rather than fix it.
func TestInstallOverwritesWhateverWasThereBefore(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH, so install cannot be exercised here")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	installDir := t.TempDir()
	stalePath := filepath.Join(installDir, "fleetdeck-status")
	if err := os.WriteFile(stalePath, []byte("not a real binary, standing in for a stale install"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleSum := sha256.Sum256([]byte("not a real binary, standing in for a stale install"))

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "make", "install", "VERSION=1.0.0-overwrite-check", "INSTALLDIR="+installDir)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make install: %v\n%s", err, out)
	}

	body, err := os.ReadFile(stalePath)
	if err != nil {
		t.Fatal(err)
	}
	gotSum := sha256.Sum256(body)
	if gotSum == staleSum {
		t.Fatal("install left the stale placeholder in place instead of overwriting it")
	}
}
