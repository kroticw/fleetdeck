package review

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repo is a scratch repository on master with one commit, and a function that
// runs git in it the way a test needs to: failing the test on any error.
func repo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet", "--initial-branch=master")
	run("config", "user.email", "t@example.invalid")
	run("config", "user.name", "T")
	run("config", "commit.gpgsign", "false")
	write(t, dir, "a.go", "package a\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "base")
	return dir, run
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheBaseIsTheForkPointFromTheDefaultBranch(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	fork := run("rev-parse", "HEAD")
	run("update-ref", "refs/remotes/origin/master", fork)
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	run("switch", "--quiet", "--create", "work")
	write(t, dir, "a.go", "package a\n\nfunc A() {}\n")
	run("commit", "--quiet", "--all", "--message", "work")

	b, err := Git{Dir: dir}.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if b.Commit != fork || b.Ref != "origin/master" {
		t.Fatalf("base = %+v, want %s on origin/master", b, fork)
	}
}

func TestAMasterMergedIntoTheBranchMovesTheBase(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	run("update-ref", "refs/remotes/origin/master", run("rev-parse", "HEAD"))
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	run("switch", "--quiet", "--create", "work")
	write(t, dir, "w.go", "package a\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "work")
	run("switch", "--quiet", "master")
	write(t, dir, "m.go", "package a\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "master moves")
	moved := run("rev-parse", "HEAD")
	run("update-ref", "refs/remotes/origin/master", moved)
	run("switch", "--quiet", "work")
	run("merge", "--quiet", "--no-edit", "master")

	b, err := Git{Dir: dir}.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if b.Commit != moved {
		t.Fatalf("base = %s, want the merged master %s", b.Commit, moved)
	}
}

func TestADivergedLocalDefaultBranchLosesToTheRemoteOne(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	root := run("rev-parse", "HEAD")
	run("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	run("switch", "--quiet", "--create", "work")
	write(t, dir, "w.go", "package a\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "work")
	// The remote's master and the local one each got a commit the other never
	// saw, and the branch merged both: neither fork point descends from the
	// other.
	run("switch", "--quiet", "--create", "remote-line", root)
	write(t, dir, "o.go", "package a\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "remote only")
	remote := run("rev-parse", "HEAD")
	run("update-ref", "refs/remotes/origin/master", remote)
	run("switch", "--quiet", "master")
	write(t, dir, "l.go", "package a\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "local only")
	run("switch", "--quiet", "work")
	run("merge", "--quiet", "--no-edit", "master")
	run("merge", "--quiet", "--no-edit", "remote-line")

	b, err := Git{Dir: dir}.Base(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if b.Ref != "origin/master" || b.Commit != remote {
		t.Fatalf("base = %+v, want origin/master at %s", b, remote)
	}
}

func TestNoDefaultBranchIsRefusedRatherThanGuessed(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	run("update-ref", "refs/remotes/origin/main", run("rev-parse", "HEAD"))
	run("update-ref", "refs/remotes/origin/master", run("rev-parse", "HEAD"))

	_, err := Git{Dir: dir}.Base(t.Context())
	if !errors.Is(err, ErrNoDefaultBranch) {
		t.Fatalf("err = %v, want ErrNoDefaultBranch", err)
	}
}

func TestACyrillicPathComesBackAsWritten(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	write(t, dir, "docs/ревью файла.md", "строка\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "cyrillic")
	g := Git{Dir: dir}
	raw, err := g.RawDiff(t.Context(), run("rev-parse", "HEAD~1"), "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "docs/ревью файла.md") {
		t.Fatalf("the path must not be quoted:\n%s", raw)
	}
}

func TestAMissingCommitIsRecognisedAsSuch(t *testing.T) {
	t.Parallel()
	dir, _ := repo(t)
	_, err := Git{Dir: dir}.RawDiff(context.Background(), strings.Repeat("1", 40), "HEAD", true)
	if !IsMissingObject(err) {
		t.Fatalf("err = %v, want a missing object", err)
	}
}

// t.Setenv forbids t.Parallel().
func TestAMissingCommitIsRecognisedAsSuchUnderARussianLocale(t *testing.T) {
	t.Setenv("LC_ALL", "ru_RU.UTF-8")
	t.Setenv("LANG", "ru_RU.UTF-8")
	dir, _ := repo(t)
	_, err := Git{Dir: dir}.RawDiff(context.Background(), strings.Repeat("1", 40), "HEAD", true)
	if !IsMissingObject(err) {
		t.Fatalf("err = %v, want a missing object even under a Russian locale", err)
	}
}

// A commit stored in an anchor comes back out of comments.json and straight
// into these two calls. A comment saved before this fix carries whatever the
// operator's click resolved to, which is ordinary git output and never an
// option string on its own — but nothing stops a crafted or replayed
// comments.json from holding one, and RawDiff/Lines must refuse to run it as
// an option rather than a revision. --end-of-options is what makes the two
// tests below fail the same way a real revision typo does, instead of
// quietly doing whatever the option says.

func TestRawDiffRefusesARevisionThatLooksLikeAnOption(t *testing.T) {
	t.Parallel()
	dir, _ := repo(t)
	scratch := t.TempDir()
	out := filepath.Join(scratch, "y")
	g := Git{Dir: dir}
	if _, err := g.RawDiff(context.Background(), "--output="+out, "HEAD", false); err == nil {
		t.Fatal("want an error: an option-like revision must not run as an option")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("git must not have written anything into %s, found %v", scratch, entries)
	}
}

func TestLinesRefusesACommitThatLooksLikeAnOption(t *testing.T) {
	t.Parallel()
	dir, _ := repo(t)
	scratch := t.TempDir()
	out := filepath.Join(scratch, "y")
	g := Git{Dir: dir}
	if _, err := g.Lines(context.Background(), "--output="+out, "a.go"); err == nil {
		t.Fatal("want an error: an option-like commit must not run as an option")
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("git must not have written anything into %s, found %v", scratch, entries)
	}
}

// The tree is the agent's, and its git configuration may change how a diff is
// printed. None of it may change what ParseDiff reads out of RawDiff: a blank
// context line printed as "" shifts every later line number, a missing a/ b/
// prefix turns "a/y.txt" into a rename, and a nonzero inter-hunk context glues
// trace hunks together with lines that never changed.
func TestTheTreesDiffConfigurationDoesNotChangeTheParsedDiff(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	body := func(changed ...string) string {
		lines := []string{"one", "", "two", "three", "", "four", "five", "six", "", "seven"}
		for i := 0; i+1 < len(changed); i += 2 {
			for j, l := range lines {
				if l == changed[i] {
					lines[j] = changed[i+1]
				}
			}
		}
		return strings.Join(lines, "\n") + "\n"
	}
	write(t, dir, "a/y.txt", body())
	run("add", ".")
	run("commit", "--quiet", "--message", "y")
	from := run("rev-parse", "HEAD")
	write(t, dir, "a/y.txt", body("two", "TWO", "six", "SIX"))
	run("commit", "--quiet", "--all", "--message", "change")

	g := Git{Dir: dir}
	parse := func(trace bool) []DiffFile {
		t.Helper()
		raw, err := g.RawDiff(t.Context(), from, "HEAD", trace)
		if err != nil {
			t.Fatal(err)
		}
		files, err := ParseDiff(raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	plain, plainTrace := parse(false), parse(true)
	if len(plain) != 1 || plain[0].OldPath != "a/y.txt" || plain[0].NewPath != "a/y.txt" {
		t.Fatalf("plain files = %+v", plain)
	}
	if len(plainTrace[0].Hunks) != 2 {
		t.Fatalf("plain trace hunks = %+v, want two", plainTrace[0].Hunks)
	}

	run("config", "diff.suppressBlankEmpty", "true")
	run("config", "diff.noprefix", "true")
	run("config", "diff.interHunkContext", "5")
	if got := parse(false); !reflect.DeepEqual(got, plain) {
		t.Fatalf("configured diff = %+v\nwant %+v", got, plain)
	}
	if got := parse(true); !reflect.DeepEqual(got, plainTrace) {
		t.Fatalf("configured trace = %+v\nwant %+v", got, plainTrace)
	}
}

// Git.Dir may sit in a subdirectory of the repository (the panel's worktree
// layout), and diff.relative shortens diff paths to that subdirectory while
// Lines (git show commit:path) always reads paths from the root: an anchor
// saved from one and looked up with the other would miss.
func TestRawDiffIgnoresDiffRelative(t *testing.T) {
	t.Parallel()
	dir, run := repo(t)
	write(t, dir, "sub/file.go", "package sub\n")
	run("add", ".")
	run("commit", "--quiet", "--message", "add sub/file.go")
	from := run("rev-parse", "HEAD")
	write(t, dir, "sub/file.go", "package sub\n\nfunc F() {}\n")
	run("commit", "--quiet", "--all", "--message", "change sub/file.go")
	run("config", "diff.relative", "true")

	g := Git{Dir: filepath.Join(dir, "sub")}
	raw, err := g.RawDiff(t.Context(), from, "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "a/sub/file.go") {
		t.Fatalf("diff paths must not be shortened by diff.relative:\n%s", raw)
	}
	files, err := ParseDiff(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].NewPath != "sub/file.go" {
		t.Fatalf("files = %+v, want NewPath sub/file.go", files)
	}
}
