package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LsregisterPath is the system's own tool for the LaunchServices database, by
// absolute path: it lives in macOS itself, and nothing is looked up on PATH.
const LsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"

// StandBundleID is the bundle identifier of every app bundle a test or a stand
// builds, never the app's own (dev.fleetdeck.window). LaunchServices keeps
// every bundle it is shown under its identifier, and opening "fleetdeck" --
// from Spotlight, `open -a`, `open -b` -- may open any of them: on 2026-09-14
// the operator's machine held dozens of stand bundles under the app's own
// identifier. A stand called something else can never be opened as the app.
const StandBundleID = "dev.fleetdeck.stand"

// lsregisterTimeout bounds one call. Measured on 2026-09-14 on the operator's
// machine: registering a bundle took 20-25 ms, forgetting one 10-13 ms, and a
// full dump well under a second; a call past this is a database that is not
// answering.
const lsregisterTimeout = 30 * time.Second

// LaunchServices is the Registry of macOS: the database behind which app
// opens for a bundle identifier or an app name.
//
// Why an update needs it. Measured on 2026-09-14 with a probe bundle of its
// own identifier, the incident replayed: the installed bundle opened once
// through LaunchServices, a new bundle started from .fleetdeck-update by exec
// (as the new window is), the two swapped while it ran. Afterwards
// NSWorkspace's urlForApplication, `open -b` and `open -a` all opened the
// staged path, which held the old version by then; neither the newer version
// nor the installed path won. A Dock-style bookmark resolved by path, to the
// installed bundle. Forgetting the staged path, or removing it, made all three
// open the installed bundle.
type LaunchServices struct {
	Lsregister string
}

// notRegistered is what lsregister -u says, and exits 1 with, for a bundle the
// database does not hold: kLSApplicationNotFoundErr. Measured on 2026-09-14,
// forgetting a bundle a test had built and never opened.
const notRegistered = "-10814"

// Forget takes bundle out of the database. A bundle it does not hold is
// already what Forget is for.
func (l LaunchServices) Forget(bundle string) error {
	err := l.run("-u", bundle)
	if err != nil && strings.Contains(err.Error(), notRegistered) {
		return nil
	}
	return err
}

// Register puts bundle into the database, or refreshes it there.
func (l LaunchServices) Register(bundle string) error {
	return l.run("-f", bundle)
}

// Registered says whether the database holds bundle, by its whole path. The
// database keeps paths with their symlinks resolved -- /private/var, never
// /var, measured the first time this ran -- so bundle is resolved too.
func (l LaunchServices) Registered(bundle string) (bool, error) {
	if resolved, err := filepath.EvalSymlinks(bundle); err == nil {
		bundle = resolved
	}
	ctx, cancel := context.WithTimeout(context.Background(), lsregisterTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, l.Lsregister, "-dump").Output()
	if err != nil {
		return false, fmt.Errorf("lsregister -dump: %w", err)
	}
	for _, path := range registeredPaths(out) {
		if path == bundle {
			return true, nil
		}
	}
	return false, nil
}

func (l LaunchServices) run(flag, bundle string) error {
	ctx, cancel := context.WithTimeout(context.Background(), lsregisterTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, l.Lsregister, flag, bundle).CombinedOutput(); err != nil {
		return fmt.Errorf("lsregister %s %s: %w: %s", flag, bundle, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// registeredPaths is every bundle path in an lsregister dump. A record's path
// line reads "path:" and spaces, the path, and the record's id in parentheses.
func registeredPaths(dump []byte) []string {
	var paths []string
	sc := bufio.NewScanner(bytes.NewReader(dump))
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		rest, ok := strings.CutPrefix(sc.Text(), "path:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if i := strings.LastIndex(rest, " ("); i >= 0 && strings.HasSuffix(rest, ")") {
			rest = rest[:i]
		}
		paths = append(paths, rest)
	}
	return paths
}
