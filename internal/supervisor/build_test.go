package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func makeTools(t *testing.T) Tools {
	t.Helper()
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("no make on this machine")
	}
	return Tools{Make: makeBin}
}

func TestMakeRunsTheTargetsInTheTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("one:\n\ttouch one\ntwo:\n\ttouch two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Make(context.Background(), dir, makeTools(t), os.Environ(), "one", "two"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"one", "two"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("target %s did not run", f)
		}
	}
}

// Make itself, not only BuildEnv, has to carry go to the recipes: run under
// the Dock's four directories, a recipe that calls go must still find it.
// The first version of these tests ran make with this machine's own PATH,
// where go is found anyway, and a Make that ignored BuildEnv passed them all
// -- measured by mutation.
func TestMakeCarriesGoToTheRecipesUnderTheDockPath(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on this machine")
	}
	tools := makeTools(t)
	tools.Go = goBin
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("probe:\n\tgo version\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Make(context.Background(), dir, tools, []string{"HOME=" + os.Getenv("HOME"), "PATH=" + systemPath}, "probe"); err != nil {
		t.Fatalf("make under the Dock PATH could not run go: %v", err)
	}
}

// A failed build is reported with what the build said, not as a bare exit
// code: the person reading it has to know whether it was the compiler, a
// missing tool, or a test.
func TestAFailedBuildSaysWhatTheBuildSaid(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("broken:\n\t@echo 'undefined: Frobnicate' >&2; exit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Make(context.Background(), dir, makeTools(t), os.Environ(), "broken")
	if err == nil {
		t.Fatal("a failing build reported success")
	}
	if !strings.Contains(err.Error(), "undefined: Frobnicate") {
		t.Fatalf("err = %v, want the build's own message in it", err)
	}
}
