package supervisor

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Tree is the source tree the panel is built from, and the branch of its
// remote it follows.
//
// The tree is somebody's working copy. An update only ever fast-forwards it:
// on another branch, with edits to tracked files, or with commits of its own,
// bringing it forward would mean deciding what to do with somebody's work, so
// the update refuses, says why, and leaves the tree exactly as it was.
type Tree struct {
	Dir    string
	Remote string // "origin"
	Branch string // "master"
	Git    string // absolute path, see FindTools
	Env    []string
}

// Status is where the tree stands against its remote branch.
type Status struct {
	Behind   int    // commits on the remote branch the tree does not have
	Head     string // the tree's commit
	Upstream string // the remote branch's commit, as of the last fetch
}

// WrongBranchError: the tree is checked out on a branch other than the one
// it follows.
type WrongBranchError struct{ Want, Have string }

func (e *WrongBranchError) Error() string {
	return fmt.Sprintf("the source tree is on branch %q, not %q; switch it back to update", e.Have, e.Want)
}

// DirtyError: tracked files in the tree have uncommitted edits.
type DirtyError struct{ Files []string }

func (e *DirtyError) Error() string {
	return fmt.Sprintf("the source tree has uncommitted edits (%s); commit or discard them to update", strings.Join(e.Files, ", "))
}

// DivergedError: the tree has commits the remote branch does not, so it
// cannot be moved forward without deciding what happens to them.
type DivergedError struct{ Ahead int }

func (e *DivergedError) Error() string {
	return fmt.Sprintf("the source tree has %d commit(s) of its own that the remote does not have; it cannot be fast-forwarded", e.Ahead)
}

// Check fetches the remote branch and says how far behind the tree is. It
// refuses in words -- a *WrongBranchError, *DirtyError or *DivergedError --
// when the tree is not one an update may move.
func (t *Tree) Check(ctx context.Context) (Status, error) {
	if err := t.refuseIfNotMovable(ctx); err != nil {
		return Status{}, err
	}
	if _, err := t.git(ctx, "fetch", "--quiet", t.Remote, t.Branch); err != nil {
		return Status{}, err
	}
	return t.status(ctx)
}

// Forward checks the tree and fast-forwards it to the remote branch. It never
// merges, rebases or resets: anything other than a fast-forward is refused.
func (t *Tree) Forward(ctx context.Context) (Status, error) {
	st, err := t.Check(ctx)
	if err != nil {
		return Status{}, err
	}
	if st.Behind == 0 {
		return st, nil
	}
	if _, err := t.git(ctx, "merge", "--ff-only", "--quiet", t.upstreamRef()); err != nil {
		return Status{}, err
	}
	return t.status(ctx)
}

func (t *Tree) refuseIfNotMovable(ctx context.Context) error {
	branch, err := t.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		// A detached HEAD is on no branch at all.
		return &WrongBranchError{Want: t.Branch, Have: "(detached)"}
	}
	if branch != t.Branch {
		return &WrongBranchError{Want: t.Branch, Have: branch}
	}
	// Untracked files are not edits to anything the build reads, and a stray
	// file must not block every update; only tracked changes count, staged or
	// not. `diff --name-only` rather than `status --porcelain`: porcelain lines
	// open with a status column that may be a space, and trimming the output
	// ate the first letter of the first file name -- caught by the test that
	// names the file, not assumed.
	out, err := t.git(ctx, "diff", "--name-only", "HEAD")
	if err != nil {
		return err
	}
	if out != "" {
		return &DirtyError{Files: strings.Split(out, "\n")}
	}
	return nil
}

func (t *Tree) status(ctx context.Context) (Status, error) {
	head, err := t.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return Status{}, err
	}
	upstream, err := t.git(ctx, "rev-parse", t.upstreamRef())
	if err != nil {
		return Status{}, err
	}
	counts, err := t.git(ctx, "rev-list", "--left-right", "--count", "HEAD..."+t.upstreamRef())
	if err != nil {
		return Status{}, err
	}
	ahead, behind, err := parseCounts(counts)
	if err != nil {
		return Status{}, err
	}
	if ahead > 0 {
		return Status{}, &DivergedError{Ahead: ahead}
	}
	return Status{Behind: behind, Head: head, Upstream: upstream}, nil
}

func (t *Tree) upstreamRef() string { return t.Remote + "/" + t.Branch }

// parseCounts reads `git rev-list --left-right --count A...B`: "<ahead>\t<behind>".
func parseCounts(s string) (ahead, behind int, err error) {
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", s)
	}
	if ahead, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", s)
	}
	if behind, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("unexpected rev-list output %q", s)
	}
	return ahead, behind, nil
}

func (t *Tree) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, t.Git, append([]string{"-C", t.Dir}, args...)...)
	cmd.Env = t.Env
	out, err := cmd.Output()
	if err != nil {
		detail := ""
		if ee, ok := err.(*exec.ExitError); ok {
			detail = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %w %s", args[0], err, detail)
	}
	return strings.TrimSpace(string(out)), nil
}
