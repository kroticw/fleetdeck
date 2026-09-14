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
	// StartKeeper runs Keeper, from the moment the old panel is stopped.
	StartKeeper func()
	// Events are the keeper's events.
	Events <-chan Event
	// Registry is LaunchServices; nil where there is none.
	Registry Registry
	// LockPath is the update lock the old window holds while it runs the update.
	LockPath string
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

	if err := Swap(t.Staged, t.Canonical); err != nil {
		return fail(err)
	}
	report(StepSwapped, t.Canonical)
	t.reregister()

	t.Keeper.Restart(PanelIn(t.Canonical))
	if err := t.waitOwnAnswer(ctx); err != nil {
		// The canonical bundle is the new one now, and it is the one that just
		// answered from the staged path: the old window, starting its panel
		// again, starts this build.
		report(StepFailed, err.Error())
		return err
	}
	report(StepDone, "")
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

// retireWait bounds how long the new window waits for the old one's update to
// let go of the lock before the bundle swapped out is removed. The old window
// lets go as soon as it reads done, within one look at the handover file
// (handoverPoll); this is the bound on a window that never does.
const retireWait = time.Minute

// retire removes the bundle swapped out, once the old window's update has let
// go of the lock: until then that window is still running out of it. Nothing
// goes back to that bundle -- an update that fails does so before the swap and
// leaves the canonical bundle as it was -- and left in place it is the old
// version under the app's own identifier, which LaunchServices finds again the
// next time anything walks it. The handover file and the new window's log
// beside it stay.
func (t *Takeover) retire(ctx context.Context) {
	if t.LockPath == "" {
		t.logf("no update lock to wait on; the bundle swapped out stays at %s", t.Staged)
		return
	}
	deadline := time.Now().Add(retireWait)
	for {
		release, err := Acquire(t.LockPath)
		if err == nil {
			defer release()
			break
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.logf("the bundle swapped out stays at %s: the update lock was not free: %v", t.Staged, err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(handoverPoll):
		}
	}
	if err := os.RemoveAll(t.Staged); err != nil {
		t.logf("the bundle swapped out could not be removed from %s: %v", t.Staged, err)
	}
}

// waitOwnAnswer waits for the keeper to say its own panel answers.
func (t *Takeover) waitOwnAnswer(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-t.Events:
			switch {
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
	resp, err := (&http.Client{Timeout: answerTimeout}).Do(req)
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
