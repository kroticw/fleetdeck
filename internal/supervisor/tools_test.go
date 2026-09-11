package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An app started from the Dock gets no PATH of its own: launchd hands it the
// system's four directories, /usr/bin:/bin:/usr/sbin:/sbin. On the operator's
// machine go lives in /usr/local/go/bin, outside all four -- measured with
// `launchctl getenv PATH`, which is empty. An update that runs `make build`
// the way a terminal does fails there with "go: command not found", exactly
// where it works for whoever tested it from a shell.

// systemPath is what launchd gives an app from the Dock.
const systemPath = "/usr/bin:/bin:/usr/sbin:/sbin"

func existsOnly(paths ...string) func(string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestFindToolsPrefersThePathsWrittenInAtBuildTime(t *testing.T) {
	embedded := Tools{Git: "/opt/x/git", Go: "/opt/x/go", Make: "/opt/x/make"}
	got, err := FindTools(embedded, existsOnly("/opt/x/git", "/opt/x/go", "/opt/x/make", "/usr/bin/git", "/usr/bin/make"))
	if err != nil {
		t.Fatal(err)
	}
	if got != embedded {
		t.Fatalf("got %+v, want the embedded paths %+v", got, embedded)
	}
}

func TestFindToolsFallsBackToWhereInstallersPutThem(t *testing.T) {
	got, err := FindTools(Tools{}, existsOnly("/usr/bin/git", "/usr/local/go/bin/go", "/usr/bin/make"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Go != "/usr/local/go/bin/go" {
		t.Fatalf("go = %q, want the standard installer's location", got.Go)
	}
}

// An embedded path that no longer exists -- go upgraded into another
// directory, say -- is skipped rather than trusted.
func TestFindToolsSkipsAnEmbeddedPathThatIsGone(t *testing.T) {
	got, err := FindTools(Tools{Go: "/gone/go"}, existsOnly("/usr/bin/git", "/opt/homebrew/bin/go", "/usr/bin/make"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Go != "/opt/homebrew/bin/go" {
		t.Fatalf("go = %q, want the next place it exists", got.Go)
	}
}

// Not finding go is said in words, naming the tool -- not left to surface as
// a make failure three steps later.
func TestFindToolsNamesTheToolItCannotFind(t *testing.T) {
	_, err := FindTools(Tools{}, existsOnly("/usr/bin/git", "/usr/bin/make"))
	if err == nil || !strings.Contains(err.Error(), "go") {
		t.Fatalf("err = %v, want one naming go", err)
	}
}

func TestBuildEnvPutsGoWhereMakeWillLookForIt(t *testing.T) {
	env := BuildEnv(Tools{Go: "/usr/local/go/bin/go", Git: "/usr/bin/git", Make: "/usr/bin/make"}, []string{"HOME=/Users/x", "PATH=" + systemPath})
	path := lookup(env, "PATH")
	if !strings.HasPrefix(path, "/usr/local/go/bin:") {
		t.Fatalf("PATH = %q, want go's directory first", path)
	}
	if !strings.Contains(path, systemPath) {
		t.Fatalf("PATH = %q, lost the system directories", path)
	}
	if lookup(env, "HOME") != "/Users/x" {
		t.Fatal("HOME was dropped: go needs it for its build cache")
	}
}

// The trap itself, both ends, with a real make: a Makefile whose recipe runs
// `go version` the way this repository's Makefile runs `go build`.
func TestMakeUnderTheDockPathFindsGoOnlyThroughBuildEnv(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on this machine's PATH to point the test at")
	}
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("no make on this machine")
	}
	if filepath.Dir(goBin) == "/usr/bin" || filepath.Dir(goBin) == "/bin" {
		t.Skip("go sits inside the system PATH here, so the trap cannot be reproduced")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("probe:\n\tgo version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := []string{"HOME=" + os.Getenv("HOME"), "PATH=" + systemPath}

	// Control: the environment an app from the Dock has, with nothing added.
	// Where some go already sits in the system directories -- the Linux CI
	// runner keeps an older one in /usr/bin -- there is no "go not found" to
	// show; that half is the macOS leg's, the operator's platform, and the
	// older-go half is TestAnOlderGoOnTheDockPathDoesNotWin's, everywhere.
	bare := exec.Command(makeBin, "-C", dir, "probe")
	bare.Env = base
	if out, err := bare.CombinedOutput(); err == nil {
		t.Skipf("the bare Dock PATH finds a go here (%s), so there is no go-not-found trap to show", strings.TrimSpace(string(out)))
	}

	withEnv := exec.Command(makeBin, "-C", dir, "probe")
	withEnv.Env = BuildEnv(Tools{Go: goBin, Make: makeBin}, base)
	if out, err := withEnv.CombinedOutput(); err != nil {
		t.Fatalf("make still could not run go through BuildEnv: %v\n%s", err, out)
	}
}

// The other way the Dock's PATH goes wrong: a go is there, but not the one
// the build needs -- an older one, left by the system or a package manager.
// BuildEnv puts the found go's directory first, so it wins. Shown with a
// stand-in "older go" placed on the PATH ahead of the system directories.
func TestAnOlderGoOnTheDockPathDoesNotWin(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on this machine's PATH to point the test at")
	}
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("no make on this machine")
	}

	const older = "go version go1.0-stand-in"
	oldDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(oldDir, "go"), []byte("#!/bin/sh\necho '"+older+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("probe:\n\t@go version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := []string{"HOME=" + os.Getenv("HOME"), "PATH=" + oldDir + ":" + systemPath}

	realCmd := exec.Command(goBin, "version")
	realCmd.Env = base
	realOut, err := realCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(realOut))

	// Control: without BuildEnv the stand-in wins. Were it not so, the case
	// below would pass for any environment at all.
	bare := exec.Command(makeBin, "-C", dir, "probe")
	bare.Env = base
	if out, _ := bare.CombinedOutput(); !strings.Contains(string(out), older) {
		t.Fatalf("the stand-in did not win without BuildEnv (%s): this case cannot see the trap", out)
	}

	withEnv := exec.Command(makeBin, "-C", dir, "probe")
	withEnv.Env = BuildEnv(Tools{Go: goBin, Make: makeBin}, base)
	out, err := withEnv.CombinedOutput()
	if err != nil {
		t.Fatalf("make failed under BuildEnv: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), want) || strings.Contains(string(out), older) {
		t.Fatalf("make ran %q, want %q (%s)", strings.TrimSpace(string(out)), want, goBin)
	}
}

func lookup(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}
