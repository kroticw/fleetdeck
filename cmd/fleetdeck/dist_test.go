package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// distVersion is the version the archives under test are built with. It is
// deliberately not "dev": "dev" is what a binary prints when -ldflags never reached
// the compiler, so a test using it would pass just as happily against a release that
// carries no version at all.
const distVersion = "v0.1.0"

// wantArchiveMembers is what a release archive is allowed to contain. Both commands,
// nothing else: no AppleDouble "._" companions, no .DS_Store, no staging directory.
var wantArchiveMembers = []string{"fleetdeck", "fleetdeck-status"}

// TestDistBuildsVerifiedArchives runs the real `make dist` and interrogates what it
// wrote, rather than trusting the target's own verification script — that script is
// one edit away from checking nothing, and the archives it blesses are published
// without anyone looking at them again.
//
// The staging directory is seeded with an archive from an older version first,
// because that is the case with the highest cost and the least visibility: `make dist`
// writes into dist/ and the release workflow uploads everything matching
// dist/*.tar.gz, so a leftover from a re-tag, a retry, or yesterday's build is
// published under today's tag, named after a version nobody released.
func TestDistBuildsVerifiedArchives(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH, so the release archives cannot be built here")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	distDir := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(distDir, "fleetdeck-v0.0.1-darwin-arm64.tar.gz")
	if err := os.WriteFile(stale, []byte("an archive from a version nobody is releasing"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()

	dist := exec.CommandContext(ctx, "make", "dist", "VERSION="+distVersion, "DISTDIR="+distDir)
	dist.Dir = root
	if out, err := dist.CombinedOutput(); err != nil {
		t.Fatalf("make dist: %v\n%s", err, out)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("make dist must empty its output directory: %s survived and would be published under %s", stale, distVersion)
	}

	archives := archiveNames(t, distDir)
	want := []string{
		"fleetdeck-" + distVersion + "-darwin-amd64.tar.gz",
		"fleetdeck-" + distVersion + "-darwin-arm64.tar.gz",
	}
	if strings.Join(archives, " ") != strings.Join(want, " ") {
		t.Fatalf("make dist must leave exactly the archives it built: want %v, got %v", want, archives)
	}

	for _, archive := range archives {
		members := archiveMembers(t, filepath.Join(distDir, archive))
		if strings.Join(members, " ") != strings.Join(wantArchiveMembers, " ") {
			t.Errorf("%s must contain exactly the release binaries: want %v, got %v", archive, wantArchiveMembers, members)
		}
	}

	t.Run("unpacked binary reports its version", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skipf("the archives hold darwin binaries and %s cannot run them", runtime.GOOS)
		}
		archive := filepath.Join(distDir, "fleetdeck-"+distVersion+"-darwin-"+runtime.GOARCH+".tar.gz")
		unpacked := t.TempDir()
		untar := exec.CommandContext(ctx, "tar", "--extract", "--file", archive, "--directory", unpacked)
		if out, err := untar.CombinedOutput(); err != nil {
			t.Fatalf("unpack %s: %v\n%s", archive, err, out)
		}

		out, err := exec.CommandContext(ctx, filepath.Join(unpacked, "fleetdeck"), "version").CombinedOutput()
		if err != nil {
			t.Fatalf("fleetdeck version: %v\n%s", err, out)
		}
		if got := strings.TrimSpace(string(out)); got != distVersion {
			t.Fatalf("the unpacked release binary reports %q, not %q: this archive would lie about its own version", got, distVersion)
		}
	})
}

// archiveNames lists the .tar.gz files in dir, sorted.
func archiveNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// archiveMembers lists what an archive holds, sorted.
func archiveMembers(t *testing.T, archive string) []string {
	t.Helper()
	out, err := exec.Command("tar", "--list", "--file", archive).Output()
	if err != nil {
		t.Fatalf("list %s: %v", archive, err)
	}
	members := strings.Fields(string(out))
	sort.Strings(members)
	return members
}
