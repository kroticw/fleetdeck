package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RetireLeftover removes, once, a bundle swapped out that an earlier update left
// beside the installed app at canonical: that update's new window quit while
// the old window still ran out of the bundle, so the bundle could not be removed
// then (Takeover.retire). A window started as the installed app runs this at its
// start, beside everything else; a window started from the staging directory or
// by an update never does.
//
// It removes nothing while an update runs -- the update lock is held, and the
// staging directory may hold the build being installed -- and nothing that a
// process still runs out of, such as an old window still quitting. Either is
// said, and left to the next start. Only the bundle goes: the handover file and
// the new window's log beside it stay, for whoever looks into what happened.
// Once it has gone, LaunchServices forgets its path and takes the installed one.
func RetireLeftover(ctx context.Context, canonical, lockPath string, registry Registry, logf func(format string, args ...any)) {
	staged := filepath.Join(StagingDir(canonical), BundleName)
	if _, err := os.Lstat(staged); err != nil {
		return
	}
	t := &Takeover{Staged: staged, Canonical: canonical, LockPath: lockPath, Registry: registry, Logf: logf}
	if lockPath == "" {
		t.logf("an earlier update left the bundle it swapped out at %s; with no update lock to take, it stays", staged)
		return
	}
	t.OldWindowGone = func() bool {
		running, err := runningFrom(ctx, staged)
		if err != nil {
			t.logf("whether anything still runs out of %s cannot be told: %v", staged, err)
			return false
		}
		return !running
	}
	removed, _, why := t.retireOnce()
	if !removed {
		t.logf("an earlier update left the bundle it swapped out at %s, and it stays for now: %v; the next start tries again", staged, why)
		return
	}
	t.logf("removed the bundle an earlier update swapped out at %s", staged)
	t.reregister()
}

// runningFrom says whether any process runs out of bundle: has one of the
// executables in its Contents/MacOS open, as lsof sees it, a running program's
// own executable among them.
func runningFrom(ctx context.Context, bundle string) (bool, error) {
	exes, err := filepath.Glob(filepath.Join(bundle, "Contents", "MacOS", "*"))
	if err != nil || len(exes) == 0 {
		return false, err
	}
	lsof, err := lsofPath()
	if err != nil {
		return false, err
	}
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, lsof, append([]string{"-t"}, exes...)...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	if strings.TrimSpace(out.String()) != "" {
		return true, nil
	}
	// lsof exits 1 when nothing has the files open, and says nothing.
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 && strings.TrimSpace(errOut.String()) == "" {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lsof on %s: %w %s", bundle, err, strings.TrimSpace(errOut.String()))
	}
	return false, nil
}
