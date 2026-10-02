package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/jobs"
)

const noWorktree = "the session has not said where it works: its card has no worktree field"

// reviewWorkdir answers where the session keeping a card works, for the
// review to read its branch there. The card's own worktree field first: it is
// the agent's own word. It must be absolute: a relative one would be read
// against the panel's own directory.
// Then Claude Code's job record, whose worktreePath follows the session into
// a worktree its cwd never leaves. The card's repo field is never used: it
// names a repository, and one machine holds several checkouts of it.
func reviewWorkdir(jobStore string) func(ctx context.Context, c board.Card) (string, error) {
	return func(_ context.Context, c board.Card) (string, error) {
		if c.Worktree != "" {
			if !filepath.IsAbs(c.Worktree) {
				return "", fmt.Errorf("the card's worktree field %q must be an absolute path", c.Worktree)
			}
			return c.Worktree, nil
		}
		if jobStore == "" {
			return "", errors.New(noWorktree + ", and this panel cannot find Claude Code's job store")
		}
		records, err := jobs.Load(jobStore)
		if err != nil {
			return "", fmt.Errorf(noWorktree+", and the job store cannot be read: %w", err)
		}
		i := slices.IndexFunc(records, func(r jobs.Record) bool { return r.Short == c.Session })
		if i < 0 {
			return "", fmt.Errorf(noWorktree+", and the job store has no session %s", c.Session)
		}
		rec := records[i]
		if rec.WorktreePath != "" {
			return rec.WorktreePath, nil
		}
		if rec.CWD != "" {
			return rec.CWD, nil
		}
		return "", errors.New(noWorktree + " and its job record names no directory")
	}
}
