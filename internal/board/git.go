package board

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitTimeout bounds every git invocation made by this package. Without it, a
// commit that needs a signing passphrase and finds no one to answer the
// pinentry prompt blocks forever, and the caller (an HTTP handler in a later
// task) would never return. It is a var rather than a const solely so a test
// can lower it to prove the deadline mechanism without waiting out the real
// 30 seconds; nothing outside a test may write to it.
var gitTimeout = 30 * time.Second

// gitWaitDelay bounds how long run() waits, after the timeout kills the
// process, for a child holding the output pipes open to actually exit. It is
// a var, like gitTimeout, solely so a test can shorten it alongside
// gitTimeout instead of paying the full delay; nothing outside a test may
// write to it.
var gitWaitDelay = 2 * time.Second

// Commit stages exactly one file and commits it. Other changes in the working
// tree are left alone: the panel commits what it wrote and nothing else.
//
// If the commit fails after the file has been staged, Commit makes a
// best-effort attempt to restore the index to what it found — but only for
// this file, and only if this call is the one that staged it. A file the
// human already had staged is left exactly as they left it.
func Commit(dir, file, message string) error {
	if err := validateFile(dir, file); err != nil {
		return err
	}

	if out, err := run(dir, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("not a git repository: %s: %w", strings.TrimSpace(out), err)
	}

	alreadyStaged, err := isStaged(dir, file)
	if err != nil {
		return fmt.Errorf("inspect staged changes: %w", err)
	}

	if out, err := run(dir, "add", "--", file); err != nil {
		return fmt.Errorf("stage %s: %s: %w", file, strings.TrimSpace(out), err)
	}

	staged, err := isStaged(dir, file)
	if err != nil {
		restoreIndex(dir, file, alreadyStaged)
		return fmt.Errorf("inspect staged changes: %w", err)
	}
	if !staged {
		return fmt.Errorf("nothing to commit for %s: %w", file, ErrNothingToCommit)
	}

	if out, err := run(dir, "commit", "--signoff", "--message", message, "--", file); err != nil {
		restoreIndex(dir, file, alreadyStaged)
		return fmt.Errorf("commit %s: %s: %w", file, strings.TrimSpace(out), err)
	}
	return nil
}

// validateFile rejects a file argument that is absolute or that, once
// cleaned, escapes dir — a caller is expected to pass a plain filename such
// as filepath.Base(path), and a "../" would stage something else in the
// repository entirely.
func validateFile(dir, file string) error {
	if filepath.IsAbs(file) {
		return fmt.Errorf("stage %s: must be relative to %s, not absolute", file, dir)
	}
	clean := filepath.Clean(file)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("stage %s: escapes %s", file, dir)
	}
	return nil
}

// isStaged reports whether file already has staged changes in dir's index.
func isStaged(dir, file string) (bool, error) {
	out, err := run(dir, "diff", "--cached", "--name-only", "--", file)
	if err != nil {
		return false, fmt.Errorf("%s: %w", strings.TrimSpace(out), err)
	}
	return strings.TrimSpace(out) != "", nil
}

// restoreIndex undoes the staging Commit did for file, but only when
// wasAlreadyStaged is false — if the human had the file staged before Commit
// ran, their index is left alone. This is best effort: its own failure is
// never surfaced, so it can never mask or replace the caller's real error.
func restoreIndex(dir, file string, wasAlreadyStaged bool) {
	if wasAlreadyStaged {
		return
	}
	if _, err := run(dir, "restore", "--staged", "--", file); err == nil {
		return
	}
	// A repository with no commits yet has no HEAD to restore from; fall
	// back to dropping the file from the index directly.
	_, _ = run(dir, "rm", "--cached", "--quiet", "--", file)
}

// run executes git with a bounded deadline so a stuck child process — most
// notably a commit waiting on a signing passphrase with no one to answer it
// — fails loudly instead of hanging the caller forever.
func run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = gitWaitDelay
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf

	err := cmd.Run()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return buf.String(), fmt.Errorf(
			"git %s timed out after %s, likely a commit-signing passphrase prompt with no one to answer it: %w",
			strings.Join(args, " "), gitTimeout, err)
	}
	return buf.String(), err
}
