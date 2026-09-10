package board

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return dir
}

func TestCommitStagesOnlyTheNamedFile(t *testing.T) {
	dir := initRepo(t)
	writeCard(t, dir, "a.md", sample)
	writeCard(t, dir, "b.md", sample)
	if err := Commit(dir, "a.md", "test: commit a"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ := cmd.Output()
	if !strings.Contains(string(out), "b.md") {
		t.Fatal("b.md must remain uncommitted: the panel commits only what it wrote")
	}
	if strings.Contains(string(out), "a.md") {
		t.Fatal("a.md must be committed")
	}
}

func TestCommitOutsideRepositoryIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeCard(t, dir, "a.md", sample)
	if err := Commit(dir, "a.md", "test"); err == nil {
		t.Fatal("committing outside a repository must fail loudly")
	}
}

func TestCommitWithNothingStagedIsNotSuccess(t *testing.T) {
	dir := initRepo(t)
	writeCard(t, dir, "a.md", sample)
	if err := Commit(dir, "a.md", "first"); err != nil {
		t.Fatal(err)
	}
	if err := Commit(dir, "a.md", "second"); err == nil {
		t.Fatal("a commit with nothing to commit must be reported, not silently pass")
	}
}

// TestCommitUnderRealGitConfiguration commits in a repository that inherits this
// machine's actual global git configuration, rather than the synthetic repo the
// other tests build with signing switched off. Commit signing is enabled globally
// on the maintainer's machine, and a commit that needs a passphrase blocks on a
// pinentry prompt with no error and no timeout — indistinguishable, from the
// panel's side, from a panel that has hung. A green suite built only on repos with
// signing disabled would never see it.
func TestCommitUnderRealGitConfiguration(t *testing.T) {
	if os.Getenv("FLEETDECK_SMOKE_GIT_REAL_CONFIG") == "" {
		t.Skip("set FLEETDECK_SMOKE_GIT_REAL_CONFIG=1 to commit under this machine's real git config")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	writeCard(t, dir, "a.md", sample)

	done := make(chan error, 1)
	go func() { done <- Commit(dir, "a.md", "test: commit under real git config") }()
	select {
	case err := <-done:
		t.Logf("Commit returned in time, err=%v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("Commit did not return within 30s under the real git configuration: " +
			"a commit that blocks on a signing passphrase hangs the panel with no error")
	}
}
