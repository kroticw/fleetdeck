package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Progress is one thing an update tells the person who pressed the button.
type Progress struct {
	// Step is "check", "current", "done", "handover", "handover:<step>", or
	// whatever the Source reports while it puts the new app in place: "build"
	// for a tree, "download" and "verify" for a release.
	Step   string
	Detail string
}

// Source is where an update gets the app it is going to install.
//
// There are two, and the difference between them is the whole of this
// feature: Tree builds the new app from a checkout on this machine, and
// ReleaseSource downloads one that was built and signed elsewhere. Update
// drives either without knowing which it has, so that the path everything
// below it takes -- the lock, the staging directory beside the installed app,
// the handover to a new window, the swap -- is one path with one set of
// tests, and not two that drift apart.
type Source interface {
	// Check says what this source could update to: a commit, a release tag,
	// or "" when there is nothing newer than what runs.
	Check(ctx context.Context) (string, error)
	// Stage puts that version into dir and returns the bundle it put there,
	// ready to be started. It reports its own steps through say, which may be
	// nil.
	Stage(ctx context.Context, dir, version string, say func(Progress)) (string, error)
}

// BundleName is what the app bundle is called: on disk, and inside the
// release archive.
const BundleName = "fleetdeck.app"

// StagingDir is where an update builds: beside the canonical bundle, never in
// place, and on the same filesystem, so that Swap can exchange the two.
func StagingDir(canonical string) string {
	return filepath.Join(filepath.Dir(canonical), ".fleetdeck-update")
}

// NewWindowLog is the file beside the handover file the new window writes to.
const NewWindowLog = "new-window.log"

// PanelIn is the panel inside an app bundle.
func PanelIn(bundle string) string {
	return filepath.Join(bundle, "Contents", "MacOS", "fleetdeck")
}

// Update is one press of the update button, as the running window does it:
// get the new app to the side of the installed one, start the new window from
// it, and wait for it to take the panel over and put itself in place. Where
// the new app comes from is the Source's business -- a checkout brought
// forward and built, or a release downloaded and checked. The running window
// never touches the canonical bundle; the new one does, and only once it has
// shown it works (see Takeover).
type Update struct {
	Source    Source
	Canonical string // the installed app bundle
	LockPath  string
	// HandoverTimeout bounds the new window's whole takeover.
	HandoverTimeout time.Duration
	// Launch starts the new window from the staged bundle, telling it where
	// the canonical bundle and the handover file are; stop ends it.
	Launch func(staged, canonical, handover string) (stop func(), err error)
	// Pause and Resume are the running window's keeper: paused just before
	// the new window takes the panel -- or it would start the old panel again
	// the moment the new window stops it -- and resumed if the handover fails.
	Pause, Resume func()
	Progress      func(Progress)
}

func (u *Update) say(step, detail string) {
	if u.Progress != nil {
		u.Progress(Progress{Step: step, Detail: detail})
	}
}

// Run runs the update. It returns ErrBusy when another update of the same
// tree is running, and nil both when the update is done and when there was
// nothing to update (Progress says which).
func (u *Update) Run(ctx context.Context) error {
	release, err := Acquire(u.LockPath)
	if err != nil {
		return err
	}
	defer release()

	u.say("check", "")
	version, err := u.Source.Check(ctx)
	if err != nil {
		return err
	}
	if version == "" {
		u.say("current", "")
		return nil
	}

	staging := StagingDir(u.Canonical)
	// What an earlier update left there -- the bundle it swapped out -- is
	// this program's own, and an older version of it. Starting that would be
	// an update backwards.
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clear %s: %w", staging, err)
	}
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("make %s: %w", staging, err)
	}
	staged, err := u.Source.Stage(ctx, staging, version, func(p Progress) { u.say(p.Step, p.Detail) })
	if err != nil {
		return err
	}
	if _, err := os.Stat(PanelIn(staged)); err != nil {
		return fmt.Errorf("the new app has no panel in %s: %w", staged, err)
	}

	u.say("handover", "")
	h := Handover{Path: filepath.Join(staging, "handover")}
	u.Pause()
	stop, err := u.Launch(staged, u.Canonical, h.Path)
	if err != nil {
		u.Resume()
		return fmt.Errorf("start the new window: %w", err)
	}
	hctx, cancel := context.WithTimeout(ctx, u.HandoverTimeout)
	last, detail, err := h.Watch(hctx, func(s Step, d string) { u.say("handover:"+string(s), d) })
	cancel()
	if last == StepDone {
		u.say("done", version)
		return nil
	}
	stop()
	u.Resume()
	// The person is shown this, and pressing again would only fail the same
	// way: it says where the new window wrote why.
	logPath := filepath.Join(staging, NewWindowLog)
	if err != nil {
		return fmt.Errorf("the new window did not finish taking over within %s: %w; its log: %s", u.HandoverTimeout, err, logPath)
	}
	return fmt.Errorf("the new window could not take over: %s; its log: %s", detail, logPath)
}

// Takeover is the new window's half of an update: stop the panel that runs,
// start its own from the staged bundle, and -- once that panel answers with
// this build -- swap the staged bundle into the canonical path and restart the
// panel from there. Every step is reported in the handover file. Until the
// swap, the canonical bundle is the old one and any failure leaves it so; the
// staged panel is stopped before a failure is reported, so the old window can
// start its panel again.
//
// So one update starts two panels, and shows in the panel's log as two
// identical "shutting down"/"listening" pairs a moment apart -- which reads
// as two crashes, and was read that way by the operator on 2026-09-12. Both
// starts are needed, and the order is not free: the first cannot be from the
// canonical path, because the old version is still there and stays there
// until this build has shown it works, and the second is what puts the panel
// on the path the app is installed at -- the only run of the canonical bundle
// from its final path before done is reported, and the path the panel then
// reports to everything that asks. docs/engineering/window-and-panel.md,
// "The panel is started twice", has the whole of it.
type Takeover struct {
	URL       string
	Handover  Handover
	Staged    string // the bundle this window runs from
	Canonical string
	Revision  string // the build this window is; its panel must report the same
	Keeper    *Keeper
	// Deadline is when the old window gives up on this takeover and stops this
	// window; zero is none. Run holds the keeper's starts inside it
	// (StartTimeout) and makes no swap without room left in it to start the
	// panel again.
	Deadline time.Time
	phase    takeoverPhase
	// StartKeeper runs Keeper, from the moment the old panel is stopped.
	StartKeeper func()
	// Events are the keeper's events.
	Events <-chan Event
	// Registry is LaunchServices; nil where there is none.
	Registry Registry
	// LockPath is the update lock the old window holds while it runs the update.
	LockPath string
	// OldWindowGone reports whether the window that started this one has
	// exited. The old window lets go of the lock when its update returns and
	// quits only after that, running out of the bundle swapped out until it
	// has; so the bundle is removed only once this says so. Nil counts as gone.
	OldWindowGone func() bool
	// RetireWait bounds the wait for the lock and the old window; zero is
	// retireWait.
	RetireWait time.Duration
	// Done is called once StepDone is reported.
	Done func()
	// Logf says what went wrong without failing the takeover; nil says nothing.
	Logf func(format string, args ...any)
}

// Registry is what keeps app bundles by identifier: LaunchServices on macOS.
type Registry interface {
	Forget(bundle string) error
	Register(bundle string) error
}

// Run performs the takeover.
func (t *Takeover) Run(ctx context.Context) error {
	report := func(s Step, detail string) { _ = t.Handover.Report(s, detail) }
	fail := func(err error) error {
		// The staged panel goes before the failure is reported, so the old
		// window can start the old one on a free port.
		_ = StopHolder(context.Background(), t.URL, stopGrace)
		report(StepFailed, err.Error())
		return err
	}
	defer t.phase.v.Store(takeoverEnded)
	report(StepAlive, "")

	if err := StopHolder(ctx, t.URL, stopGrace); err != nil {
		report(StepFailed, err.Error())
		return err
	}
	t.StartKeeper()
	if err := t.waitOwnAnswer(ctx); err != nil {
		return fail(err)
	}
	rev, err := panelRevision(ctx, t.URL)
	if err != nil {
		return fail(err)
	}
	if rev != t.Revision {
		return fail(fmt.Errorf("the new panel reports build %q, not %q", rev, t.Revision))
	}
	report(StepPanel, rev)

	// A failure after the swap leaves the installed bundle changed, so the swap
	// is made only with room left in the old window's deadline to start the
	// panel again and say how that went.
	if err := t.roomToSwap(time.Now()); err != nil {
		return fail(err)
	}
	t.phase.v.Store(afterSwap)
	if err := Swap(t.Staged, t.Canonical); err != nil {
		return fail(err)
	}
	// LaunchServices moves before swapped is said. The old window may give up on
	// the handover at any moment after that (T-060), and one given up on between
	// the swap and this would leave the staged path, which holds the old bundle
	// now, the one path LaunchServices knows for the app; the next update removes
	// that directory but never has LaunchServices forget it. Measured on
	// 2026-09-14: forgetting and registering together took 19-30 ms, inside a
	// handover whose worst stand run was 1144 ms against the old window's 2436.
	t.reregister()
	report(StepSwapped, t.Canonical)

	t.Keeper.Restart(PanelIn(t.Canonical))
	if err := t.waitOwnAnswer(ctx); err != nil {
		// The canonical bundle is the new one now, and it is the one that just
		// answered from the staged path: the old window, starting its panel
		// again, starts this build.
		report(StepFailed, err.Error())
		return err
	}
	report(StepDone, "")
	// The deadline is behind: from here the keeper's starts, a panel dying while
	// the bundle swapped out waits to be removed, get the window's own ceiling.
	t.phase.v.Store(takeoverEnded)
	if t.Done != nil {
		t.Done()
	}
	t.retire(ctx)
	return nil
}

func (t *Takeover) logf(format string, args ...any) {
	if t.Logf != nil {
		t.Logf(format, args...)
	}
}

// reregister has LaunchServices forget the staged path, which holds the bundle
// swapped out now, and take the canonical one. This window checked in from the
// staged path when it started, and a later "open fleetdeck" would otherwise
// open the version just replaced (LaunchServices). A failure is said, not
// fatal: the update itself has worked, and the bundle is removed after done
// anyway.
func (t *Takeover) reregister() {
	if t.Registry == nil {
		return
	}
	if err := t.Registry.Forget(t.Staged); err != nil {
		t.logf("LaunchServices still knows the bundle swapped out at %s: %v", t.Staged, err)
	}
	if err := t.Registry.Register(t.Canonical); err != nil {
		t.logf("LaunchServices was not told of the installed bundle at %s: %v", t.Canonical, err)
	}
}

// retireWait bounds how long the new window waits for the old one to quit and
// let go of the update lock before the bundle swapped out is removed. The old
// window's update returns as soon as it reads done, within one look at the
// handover file (handoverPoll), and the window quits right after; this is the
// bound on a window that does not. Past it the bundle stays, and says so.
const retireWait = time.Minute

// retire removes the bundle swapped out, once the old window has quit and its
// update has let go of the lock. The order matters, and the lock alone is not
// enough: the old window's update releases the lock as it returns, and the
// window quits only after that -- running, until it has, out of the very
// bundle this removes. Nothing goes back to that bundle -- an update that
// fails does so before the swap and leaves the canonical bundle as it was --
// and left in place it is the old version under the app's own identifier,
// which LaunchServices finds again the next time anything walks it. The
// handover file and the new window's log beside it stay.
func (t *Takeover) retire(ctx context.Context) {
	if err := t.retirable(); err != nil {
		t.logf("the bundle swapped out is not removed: %v", err)
		return
	}
	if t.LockPath == "" {
		t.logf("no update lock to wait on; the bundle swapped out stays at %s", t.Staged)
		return
	}
	wait := t.RetireWait
	if wait <= 0 {
		wait = retireWait
	}
	deadline := time.Now().Add(wait)
	var release func()
	for waiting := false; ; {
		if t.OldWindowGone == nil || t.OldWindowGone() {
			r, err := Acquire(t.LockPath)
			if err == nil {
				release = r
				break
			}
			if !errors.Is(err, ErrBusy) {
				t.logf("the bundle swapped out stays at %s: the update lock could not be taken: %v", t.Staged, err)
				return
			}
		}
		if !waiting {
			waiting = true
			t.logf("waiting for the old window to quit and let go of the update lock before removing the bundle swapped out at %s", t.Staged)
		}
		if time.Now().After(deadline) {
			t.logf("the bundle swapped out stays at %s: the old window had not quit and let go of the update lock within %s", t.Staged, wait)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(handoverPoll):
		}
	}
	defer release()
	// Asked again at the moment of removal: a minute may have passed.
	if err := t.retirable(); err != nil {
		t.logf("the bundle swapped out is not removed: %v", err)
		return
	}
	if err := os.RemoveAll(t.Staged); err != nil {
		t.logf("the bundle swapped out could not be removed from %s: %v", t.Staged, err)
	}
}

// retirable says why Staged may not be removed, or nil. The removal happens
// beside the installed app, in /Applications on a person's machine, and it
// checks what it removes itself rather than trusting how Staged and Canonical
// were worked out: exactly the bundle in the installed app's staging
// directory, not a symlink, and not the installed app.
func (t *Takeover) retirable() error {
	if t.Staged == "" || t.Canonical == "" {
		return fmt.Errorf("no bundle to remove was named (staged %q, installed %q)", t.Staged, t.Canonical)
	}
	info, err := os.Lstat(t.Staged)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink, not the bundle swapped out", t.Staged)
	}
	staged, err := filepath.EvalSymlinks(t.Staged)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(t.Canonical)
	if err != nil {
		return fmt.Errorf("the installed app %s: %w", t.Canonical, err)
	}
	if staged == canonical {
		return fmt.Errorf("%s is the installed app", t.Staged)
	}
	want := filepath.Join(StagingDir(t.Canonical), BundleName)
	if resolved, err := filepath.EvalSymlinks(want); err != nil || staged != resolved {
		return fmt.Errorf("%s is not %s, the bundle in the installed app's staging directory", t.Staged, want)
	}
	return nil
}

// waitOwnAnswer waits for the keeper to say its own panel answers.
func (t *Takeover) waitOwnAnswer(ctx context.Context) error {
	// The panel waited for is the one the keeper starts during this wait: an
	// answer from a panel it started before -- the staged one, while the
	// canonical one is awaited -- is not this one's, and is passed over.
	started := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-t.Events:
			switch {
			case e.State == Starting:
				started = e.PID
			case e.State == Answering && e.Ours && (started == 0 || e.PID != started):
				continue
			case e.State == Answering && e.Ours:
				return nil
			case e.State == Answering:
				return fmt.Errorf("a panel this window did not start answers at %s", t.URL)
			case e.State == Failed:
				return fmt.Errorf("%v\n%s", e.Err, e.LogTail)
			}
		}
	}
}

// panelRevision is the revision the panel at panelURL reports in its build
// fingerprint.
func panelRevision(ctx context.Context, panelURL string) (string, error) {
	u, err := url.Parse(panelURL)
	if err != nil {
		return "", err
	}
	u.Path = "/api/snapshot"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := panelClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ask the new panel for its build: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var snap struct {
		Build *struct {
			Revision string `json:"revision"`
		} `json:"build"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&snap); err != nil || snap.Build == nil {
		return "", fmt.Errorf("the new panel's snapshot carries no build (%s)", resp.Status)
	}
	return snap.Build.Revision, nil
}
