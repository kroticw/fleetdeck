// Package review is the panel's local review of a session's branch: the diff
// read with git, comments anchored to commits and traced to the current code,
// the operator's comment file and the agent's reply file on the board. Nothing
// here speaks HTTP.
package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// ErrNoDefaultBranch means the repository does not say which branch the
// session's branch forks from, and guessing master would diff against the
// wrong base without a word.
var ErrNoDefaultBranch = errors.New("the repository does not name its default branch: set it with git remote set-head origin --auto")

// Git runs read-only git commands in one working tree. The tree is the
// agent's: its configuration may name external diff drivers, textconv filters
// or an fsmonitor, and every call switches those off rather than run them.
type Git struct {
	Dir     string
	Timeout time.Duration
}

// Base is the fork point of the session's branch from the default branch,
// and the ref it was found on. Ref is shaped like "origin/master" or
// "master". Tagged lowercase for the JSON the panel's frontend reads
// (base.commit, base.ref), matching every other review type.
type Base struct {
	Commit string `json:"commit"`
	Ref    string `json:"ref"`
}

func (g Git) run(ctx context.Context, args ...string) (string, error) {
	timeout := g.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"--no-pager", "-C", g.Dir, "-c", "core.quotepath=off", "-c", "core.fsmonitor=false", "-c", "diff.suppressBlankEmpty=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	// GIT_OPTIONAL_LOCKS=0: the agent commits in this tree while the panel
	// reads it, and a status that refreshes the index takes index.lock.
	// LC_ALL=C: IsMissingObject reads git's English stderr messages, and the
	// operator's shell locale must not translate them.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// Wraps DeadlineExceeded itself, not only the killed process's
			// error: a caller tells a timeout from a refusal by it.
			return "", fmt.Errorf("git %s timed out after %s: %w (%w)", args[0], timeout, context.DeadlineExceeded, err)
		}
		return "", &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), err: err}
	}
	return stdout.String(), nil
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

// IsMissingObject reports a commit the repository no longer has: rebased away
// and collected, or never fetched here.
func IsMissingObject(err error) bool {
	var ge *gitError
	if !errors.As(err, &ge) {
		return false
	}
	for _, sign := range []string{"bad object", "unknown revision", "bad revision", "Invalid revision range"} {
		if strings.Contains(ge.stderr, sign) {
			return true
		}
	}
	return false
}

func (g Git) one(ctx context.Context, args ...string) (string, error) {
	out, err := g.run(ctx, args...)
	return strings.TrimSpace(out), err
}

func (g Git) Toplevel(ctx context.Context) (string, error) {
	return g.one(ctx, "rev-parse", "--show-toplevel")
}

func (g Git) Head(ctx context.Context) (string, error) {
	return g.one(ctx, "rev-parse", "--verify", "HEAD^{commit}")
}

func (g Git) exists(ctx context.Context, ref string) bool {
	_, err := g.one(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

func (g Git) defaultBranch(ctx context.Context) (string, error) {
	if ref, err := g.one(ctx, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(ref, "refs/remotes/origin/"), nil
	}
	var found []string
	for _, b := range []string{"main", "master"} {
		if g.exists(ctx, "refs/remotes/origin/"+b) {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		return "", ErrNoDefaultBranch
	}
	return found[0], nil
}

// Base is the fork point of HEAD from the default branch. Both the remote and
// the local copy of that branch are asked, and the later fork point wins: the
// local one may be ahead of a remote the panel never fetches, or behind it.
// When neither descends from the other, the remote's wins: it is what the
// branch will be merged into.
func (g Git) Base(ctx context.Context) (Base, error) {
	branch, err := g.defaultBranch(ctx)
	if err != nil {
		return Base{}, err
	}
	remote := "origin/" + branch
	remoteBase, err := g.one(ctx, "merge-base", "HEAD", remote)
	if err != nil {
		return Base{}, fmt.Errorf("fork point from %s: %w", remote, err)
	}
	if !g.exists(ctx, "refs/heads/"+branch) {
		return Base{Commit: remoteBase, Ref: remote}, nil
	}
	localBase, err := g.one(ctx, "merge-base", "HEAD", branch)
	if err != nil || localBase == remoteBase {
		return Base{Commit: remoteBase, Ref: remote}, nil
	}
	if _, err := g.one(ctx, "merge-base", "--is-ancestor", remoteBase, localBase); err == nil {
		return Base{Commit: localBase, Ref: branch}, nil
	}
	return Base{Commit: remoteBase, Ref: remote}, nil
}

// RawDiff is the diff between two commits. trace is the diff comments are
// traced through: no context, whitespace-only changes ignored, so reindented
// code keeps its comments where they were.
//
// from and to are read out of a stored anchor or off the request, not typed
// by the operator into a shell, so neither is trusted to be a revision: a
// value shaped like "--output=..." is placed after --end-of-options, which
// tells git that everything past it is a revision or path and never an
// option, however it is spelled.
func (g Git) RawDiff(ctx context.Context, from, to string, trace bool) (string, error) {
	// The prefixes and the inter-hunk context are spelled out: the tree's
	// diff.noprefix or diff.srcPrefix would turn "a/y.txt" into a rename, and
	// diff.interHunkContext would glue hunks together with unchanged lines.
	// --no-relative overrides the tree's diff.relative: Dir may sit in a
	// subdirectory of the repository, and a path shortened to that
	// subdirectory would not match the root-relative path Lines reads with
	// git show commit:path, breaking every anchor.
	args := []string{
		"diff", "--no-color", "--no-ext-diff", "--no-textconv", "-M", "--no-relative",
		"--src-prefix=a/", "--dst-prefix=b/", "--inter-hunk-context=0",
	}
	if trace {
		args = append(args, "--unified=0", "--ignore-space-change")
	} else {
		args = append(args, "--unified=3")
	}
	return g.run(ctx, append(args, "--end-of-options", from, to, "--")...)
}

// Uncommitted lists the paths with changes not in HEAD, untracked included.
func (g Git) Uncommitted(ctx context.Context) ([]string, error) {
	out, err := g.run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		paths = append(paths, e[3:])
		// A rename or copy is followed by its source path as a field of its
		// own, with no status in front of it.
		if e[0] == 'R' || e[0] == 'C' {
			i++
		}
	}
	return paths, nil
}

// Lines is path's content at commit, one element per line.
//
// commit comes out of a stored anchor or off the request, so it is placed
// after --end-of-options for the same reason RawDiff's from and to are: a
// value shaped like "--output=..." must be refused as a bad revision, never
// run as an option.
func (g Git) Lines(ctx context.Context, commit, path string) ([]string, error) {
	out, err := g.run(ctx, "show", "--end-of-options", commit+":"+path)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n"), nil
}
