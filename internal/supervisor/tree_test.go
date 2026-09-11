package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real git, in throwaway repositories: a bare "origin" and a clone of it
// standing in for the tree the panel is built from. A fake git would only
// agree with whatever this package assumes git prints.
//
// Every git here runs with the global and system configuration switched off.
// The machine this runs on signs commits by default, and a test commit that
// asks for a signing PIN never finishes.

type fixture struct {
	t      *testing.T
	git    string
	env    []string
	origin string
	tree   string
	other  string // a second clone that plays whoever pushes to origin
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on this machine")
	}
	root := t.TempDir()
	f := &fixture{
		t:      t,
		git:    git,
		origin: filepath.Join(root, "origin.git"),
		tree:   filepath.Join(root, "tree"),
		other:  filepath.Join(root, "other"),
		env: append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		),
	}
	f.run(root, "init", "--quiet", "--bare", "--initial-branch=master", f.origin)
	f.run(root, "clone", "--quiet", f.origin, f.other)
	f.commit(f.other, "first")
	f.run(f.other, "push", "--quiet", "origin", "master")
	f.run(root, "clone", "--quiet", f.origin, f.tree)
	return f
}

func (f *fixture) run(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command(f.git, args...)
	cmd.Dir = dir
	cmd.Env = f.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) commit(dir, name string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.run(dir, "add", name)
	f.run(dir, "-c", "commit.gpgsign=false", "commit", "--quiet", "--message", name)
}

// pushed adds n commits to origin from the other clone.
func (f *fixture) pushed(n int) {
	f.t.Helper()
	for i := range n {
		f.commit(f.other, "upstream-"+string(rune('a'+i)))
	}
	f.run(f.other, "push", "--quiet", "origin", "master")
}

func (f *fixture) supervisedTree() *Tree {
	return &Tree{Dir: f.tree, Remote: "origin", Branch: "master", Git: f.git, Env: f.env}
}

func TestATreeThatHasFallenBehindSaysByHowMuch(t *testing.T) {
	f := newFixture(t)
	f.pushed(2)

	st, err := f.supervisedTree().Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Behind != 2 {
		t.Fatalf("behind = %d, want 2", st.Behind)
	}
	if st.Head == st.Upstream {
		t.Fatal("head and upstream are the same commit with origin two ahead")
	}
}

func TestATreeThatIsCurrentIsZeroBehind(t *testing.T) {
	f := newFixture(t)
	st, err := f.supervisedTree().Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Behind != 0 {
		t.Fatalf("behind = %d, want 0", st.Behind)
	}
}

func TestBringingATreeForwardLandsOnUpstream(t *testing.T) {
	f := newFixture(t)
	f.pushed(3)
	tree := f.supervisedTree()

	st, err := tree.Forward(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Behind != 0 || st.Head != st.Upstream {
		t.Fatalf("after Forward: %+v, want head at upstream", st)
	}
	if got := f.run(f.tree, "rev-parse", "HEAD"); got != f.run(f.other, "rev-parse", "HEAD") {
		t.Fatalf("tree HEAD %s is not origin's %s", got, f.run(f.other, "rev-parse", "HEAD"))
	}
}

// The tree the panel is built from is someone's working copy. On another
// branch, with edits, or with commits of its own, an update would have to
// decide what to do with somebody's work -- so it refuses, says why in words,
// and changes nothing.

func TestATreeOnAnotherBranchIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	f.run(f.tree, "switch", "--quiet", "--create", "experiment")
	f.pushed(1)
	before := f.run(f.tree, "rev-parse", "HEAD")

	_, err := f.supervisedTree().Forward(context.Background())
	var wrong *WrongBranchError
	if !errors.As(err, &wrong) || wrong.Have != "experiment" {
		t.Fatalf("err = %v, want a WrongBranchError naming experiment", err)
	}
	if f.run(f.tree, "rev-parse", "HEAD") != before {
		t.Fatal("the tree moved although the update refused")
	}
}

func TestATreeWithEditsIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.tree, "first"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.pushed(1)

	_, err := f.supervisedTree().Forward(context.Background())
	var dirty *DirtyError
	if !errors.As(err, &dirty) || len(dirty.Files) != 1 || dirty.Files[0] != "first" {
		t.Fatalf("err = %v, want a DirtyError naming the edited file", err)
	}
	b, _ := os.ReadFile(filepath.Join(f.tree, "first"))
	if string(b) != "edited" {
		t.Fatal("the edit was touched")
	}
}

// Untracked files are not edits to anything the build uses: a stray file in
// the tree must not stop every update.
func TestAnUntrackedFileDoesNotStopAnUpdate(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.tree, "stray.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.pushed(1)
	if _, err := f.supervisedTree().Forward(context.Background()); err != nil {
		t.Fatalf("an untracked file blocked the update: %v", err)
	}
}

func TestATreeWithCommitsOfItsOwnIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	f.commit(f.tree, "local")
	f.pushed(1)
	before := f.run(f.tree, "rev-parse", "HEAD")

	_, err := f.supervisedTree().Forward(context.Background())
	var diverged *DivergedError
	if !errors.As(err, &diverged) || diverged.Ahead != 1 {
		t.Fatalf("err = %v, want a DivergedError with one local commit", err)
	}
	if f.run(f.tree, "rev-parse", "HEAD") != before {
		t.Fatal("the tree moved although the update refused")
	}
}

// Each refusal names what is wrong in words a person can act on.
func TestRefusalsSayWhatIsWrong(t *testing.T) {
	for _, err := range []error{
		&WrongBranchError{Want: "master", Have: "experiment"},
		&DirtyError{Files: []string{"a.go", "b.go"}},
		&DivergedError{Ahead: 2},
	} {
		msg := err.Error()
		if msg == "" || strings.Contains(msg, "%!") {
			t.Errorf("%T says %q", err, msg)
		}
	}
	if !strings.Contains((&WrongBranchError{Want: "master", Have: "experiment"}).Error(), "experiment") {
		t.Error("the wrong-branch refusal does not name the branch")
	}
	if !strings.Contains((&DirtyError{Files: []string{"a.go"}}).Error(), "a.go") {
		t.Error("the dirty refusal does not name the file")
	}
	if !strings.Contains((&DivergedError{Ahead: 7}).Error(), "7 commit") {
		t.Error("the diverged refusal does not say how many commits are the tree's own")
	}
}
