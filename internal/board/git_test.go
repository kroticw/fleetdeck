package board

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	err := Commit(dir, "a.md", "second")
	if err == nil {
		t.Fatal("a commit with nothing to commit must be reported, not silently pass")
	}
	if !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("expected errors.Is(err, ErrNothingToCommit), got %v", err)
	}
}

// TestCommitFailureLeavesIndexClean rules out the case where Commit stages a
// file, the commit itself then fails (a rejected pre-commit hook here, a
// dismissed signing prompt on the real machine), and the panel leaves the
// operator's repository with a file staged that they never asked to stage.
func TestCommitFailureLeavesIndexClean(t *testing.T) {
	dir := initRepo(t)
	hooksDir := filepath.Join(dir, ".git", "hooks")
	hook := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCard(t, dir, "a.md", sample)

	if err := Commit(dir, "a.md", "test: rejected by hook"); err == nil {
		t.Fatal("a commit rejected by a pre-commit hook must return an error")
	}

	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git diff --cached: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("a failed commit must leave the index as it found it, got staged: %q", out)
	}
}

// TestCommitLeavesPreexistingStageAlone makes sure the cleanup in
// TestCommitFailureLeavesIndexClean does not overreach: if the human already
// had this file staged before Commit ran, a failed commit must not unstage
// their work.
func TestCommitLeavesPreexistingStageAlone(t *testing.T) {
	dir := initRepo(t)
	hooksDir := filepath.Join(dir, ".git", "hooks")
	hook := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCard(t, dir, "a.md", sample)

	add := exec.Command("git", "add", "--", "a.md")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}

	if err := Commit(dir, "a.md", "test: rejected by hook"); err == nil {
		t.Fatal("a commit rejected by a pre-commit hook must return an error")
	}

	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git diff --cached: %v", err)
	}
	if strings.TrimSpace(string(out)) != "a.md" {
		t.Fatalf("a file the human already staged must stay staged, got %q", out)
	}
}

// TestCommitWrapsUnderlyingError proves the errors returned by Commit wrap
// the real cause with %w, so errors.Is/errors.As can inspect it instead of
// being left with only a formatted string.
func TestCommitWrapsUnderlyingError(t *testing.T) {
	dir := initRepo(t)
	writeCard(t, dir, "a.md", sample)
	t.Setenv("PATH", "")

	err := Commit(dir, "a.md", "test")
	if err == nil {
		t.Fatal("commit must fail when git is not on PATH")
	}
	var execErr *exec.Error
	if !errors.As(err, &execErr) {
		t.Fatalf("expected the returned error to wrap an *exec.Error, got %v", err)
	}
}

func TestCommitRejectsAbsoluteFile(t *testing.T) {
	dir := initRepo(t)
	writeCard(t, dir, "a.md", sample)
	abs := filepath.Join(dir, "a.md")
	if err := Commit(dir, abs, "test"); err == nil {
		t.Fatal("an absolute file path must be rejected")
	}
}

// TestCommitRejectsFileEscapingDir uses a file that escapes dir while
// remaining inside the same git repository (dir is a subdirectory of the
// repository root, and the file lives elsewhere under that root). Git's own
// pathspec check would happily accept such a file — it is still inside the
// repository — so this must be caught by Commit's own containment check,
// not by git refusing an out-of-repository path.
func TestCommitRejectsFileEscapingDir(t *testing.T) {
	repoRoot := initRepo(t)
	cardsDir := filepath.Join(repoRoot, "cards")
	if err := os.MkdirAll(cardsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCard(t, repoRoot, "secret.md", sample)

	if err := Commit(cardsDir, "../secret.md", "test"); err == nil {
		t.Fatal("a file path that escapes dir must be rejected, even if it stays inside the same git repository")
	}
}

// TestRunEnforcesGitTimeout proves the bounded deadline in run() actually
// fires, without depending on this machine's gpg configuration: it points
// PATH at a fake "git" that just hangs, and checks that run() still returns
// — around gitTimeout, not never — with an error naming the likely cause.
//
// gitTimeout and gitWaitDelay are package-level vars (not the real 30s/2s
// consts they would otherwise be) precisely so this test can lower both
// here, rather than proving the deadline exists by waiting out the real
// production values on every CI run: the fake "git" below runs as a shell
// script that execs "sleep" as a child rather than replacing itself, so
// SIGKILL only kills the shell, and run() would otherwise wait the full
// gitWaitDelay for "sleep" itself to release the output pipes. Do not run
// this test with t.Parallel(): it mutates package-level vars, and every
// other test in this package assumes they hold still while it runs.
func TestRunEnforcesGitTimeout(t *testing.T) {
	const testTimeout = 200 * time.Millisecond
	const testWaitDelay = 50 * time.Millisecond
	originalTimeout, originalWaitDelay := gitTimeout, gitWaitDelay
	gitTimeout, gitWaitDelay = testTimeout, testWaitDelay
	t.Cleanup(func() { gitTimeout, gitWaitDelay = originalTimeout, originalWaitDelay })

	fakeGitDir := t.TempDir()
	script := "#!/bin/sh\nsleep 300\n"
	if err := os.WriteFile(filepath.Join(fakeGitDir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	bound := testTimeout + testWaitDelay + 2*time.Second

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	go func() {
		start := time.Now()
		_, err := run(dir, "status")
		done <- result{err: err, elapsed: time.Since(start)}
	}()

	var res result
	select {
	case res = <-done:
	case <-time.After(bound):
		t.Fatalf("run did not return within %s of the (shortened) gitTimeout=%s: the deadline "+
			"mechanism appears broken, not just slow", bound, gitTimeout)
	}
	err, elapsed := res.err, res.elapsed

	if err == nil {
		t.Fatal("run must return an error when git hangs past the deadline, not block forever")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected the timeout error to name its cause, got %v", err)
	}
	if elapsed < gitTimeout {
		t.Fatalf("run returned after %s, before gitTimeout=%s had even elapsed", elapsed, gitTimeout)
	}
	// bound (gitTimeout+gitWaitDelay+2s margin) covers scheduling slop on a
	// loaded CI runner without letting a genuinely broken deadline slip by
	// unnoticed; the select above already enforces it as a hard cutoff, this
	// re-checks it as a precise assertion with a clear failure message.
	if elapsed > bound {
		t.Fatalf("run took %s, expected to return at or shortly after gitTimeout=%s", elapsed, gitTimeout)
	}
}

// TestCommitUnderRealGitConfiguration commits in a repository that inherits this
// machine's actual global git configuration, rather than the synthetic repo the
// other tests build with signing switched off. Commit signing is enabled globally
// on the maintainer's machine, and a commit that needs a passphrase puts up a
// pinentry prompt with no one to answer it. With the bounded deadline in run(),
// that no longer means Commit hangs — it means Commit returns, either with a
// successful commit (a warm gpg-agent cache) or with a timeout error (a cold
// one), within gitTimeout plus a margin. This test asserts exactly that: it is
// proving the deadline exists and is enforced under the real configuration, not
// that a commit succeeds. A green suite built only on repos with signing
// disabled would never exercise the real pinentry path at all.
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
	margin := 10 * time.Second
	select {
	case err := <-done:
		t.Logf("Commit returned within gitTimeout+margin, err=%v", err)
	case <-time.After(gitTimeout + margin):
		t.Fatal("Commit did not return within gitTimeout plus a margin under the real git " +
			"configuration: the bounded deadline in run() failed to enforce itself")
	}
}

// A new board is a repository of its own on master, the branch
// bootstrap-board.sh gave the operator's board, whatever init.defaultBranch the
// machine's git carries.
func TestInitRepoMakesARepositoryOnMaster(t *testing.T) {
	dir := t.TempDir()
	if err := InitRepo(dir); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "symbolic-ref", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("not a repository after InitRepo: %s", out)
	}
	if got := strings.TrimSpace(string(out)); got != "refs/heads/master" {
		t.Fatalf("a new board starts on master: got %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("the repository must be the board's own, not an enclosing one: %v", err)
	}
}
