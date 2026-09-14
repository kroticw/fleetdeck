package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An update end to end, without a window: a real git tree whose Makefile
// "builds the app" by copying the stand-in panel into a bundle with the
// tree's revision beside it; the running window's keeper on the canonical
// bundle; and, as the new window, a Takeover with its own keeper, run in a
// goroutine by Launch. What is real: git, make, the processes, the port, the
// signals, the swap. What is not: the web views.

const fakeMakefile = "window-app:\n" +
	"\tmkdir -p \"$(BINDIR)/fleetdeck.app/Contents/MacOS\"\n" +
	"\tcp \"$(HELPER_BIN)\" \"$(BINDIR)/fleetdeck.app/Contents/MacOS/fleetdeck\"\n" +
	"\tgit rev-parse HEAD > \"$(BINDIR)/fleetdeck.app/Contents/MacOS/revision\"\n"

type updateRig struct {
	t         *testing.T
	f         *fixture
	addr      string
	url       string
	canonical string
	env       []string // the panel's environment, stand-in kind included
	oldKeeper *Keeper
	oldCancel context.CancelFunc
	oldDone   chan struct{}
	progress  []Progress
	mu        sync.Mutex
	newKind   string // the stand-in kind the new window's panel runs as
	// expect, when set, is the build the new window believes it is, in place
	// of what its bundle says.
	expect    string
	takeover  error
	oldStarts atomic.Int64 // panels the old window's keeper started
	newFrom   []string     // the bundle each panel of the new window was started from
	// registry stands in for LaunchServices, for the new window.
	registry *fakeRegistry
	// lockPath is the update lock both windows take.
	lockPath string
	// doneCalls counts the new window's Done; takeoverDone is closed when its
	// Takeover.Run has returned.
	doneCalls    int
	takeoverDone chan struct{}
}

// fakeRegistry is LaunchServices as the new window sees it: what it was told,
// in order, with the handover file as it stood at each call.
type fakeRegistry struct {
	mu       sync.Mutex
	handover string
	calls    []registryCall
}

type registryCall struct {
	op, bundle string
	steps      string // the handover file's steps at the call
}

func (f *fakeRegistry) record(op, bundle string) error {
	data, _ := os.ReadFile(f.handover)
	var steps []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		name, _, _ := strings.Cut(line, "\t")
		steps = append(steps, name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, registryCall{op: op, bundle: bundle, steps: strings.Join(steps, ",")})
	return nil
}

func (f *fakeRegistry) Forget(bundle string) error   { return f.record("forget", bundle) }
func (f *fakeRegistry) Register(bundle string) error { return f.record("register", bundle) }

func (f *fakeRegistry) told() []registryCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]registryCall(nil), f.calls...)
}

// startedFrom is where the new window's keeper started each of its panels, in
// order.
func (r *updateRig) startedFrom() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.newFrom...)
}

func newUpdateRig(t *testing.T) *updateRig {
	t.Helper()
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("no make on this machine")
	}
	needLsof(t)
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.other, "Makefile"), []byte(fakeMakefile), 0o644); err != nil {
		t.Fatal(err)
	}
	f.run(f.other, "add", "Makefile")
	f.run(f.other, "-c", "commit.gpgsign=false", "commit", "--quiet", "--message", "makefile")
	f.run(f.other, "push", "--quiet", "origin", "master")

	r := &updateRig{t: t, f: f, addr: freeAddr(t), newKind: "listen"}
	r.url = "http://" + r.addr + "/"
	r.canonical = filepath.Join(t.TempDir(), "apps", "fleetdeck.app")
	r.lockPath = filepath.Join(t.TempDir(), "update.lock")
	r.registry = &fakeRegistry{handover: filepath.Join(StagingDir(r.canonical), "handover")}
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(PanelIn(r.canonical)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PanelIn(r.canonical), self, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(PanelIn(r.canonical)), "revision"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Through helperEnvFor, like every stand-in: a stand-in whose test
	// process is gone exits by itself, and a test binary killed by its
	// -timeout runs no Cleanup.
	r.env = helperEnvFor("listen", r.addr)
	// MinUptime zero: the old window's panel has been up for hours, so its
	// keeper would start it again the moment it stops -- unless paused.
	r.oldKeeper = &Keeper{
		URL: r.url, Bin: PanelIn(r.canonical), Env: r.env,
		LogPath: filepath.Join(t.TempDir(), "old.log"), StartTimeout: 5 * time.Second,
		MinUptime: 0, Poll: 100 * time.Millisecond,
		OnEvent: func(e Event) {
			if e.State == Starting {
				r.oldStarts.Add(1)
			}
		},
	}
	r.resumeOld()
	t.Cleanup(func() {
		r.pauseOld()
		_ = StopHolder(context.Background(), r.url, time.Second)
	})
	if !waitRevision(r.url, "old", 10*time.Second) {
		t.Fatal("the old panel never answered")
	}
	return r
}

func (r *updateRig) pauseOld() {
	if r.oldCancel != nil {
		r.oldCancel()
		<-r.oldDone
		r.oldCancel = nil
	}
}

func (r *updateRig) resumeOld() {
	ctx, cancel := context.WithCancel(context.Background())
	r.oldCancel, r.oldDone = cancel, make(chan struct{})
	done := r.oldDone
	go func() { r.oldKeeper.Run(ctx); close(done) }()
}

// launch plays the new window: a Takeover with a keeper of its own.
func (r *updateRig) launch(staged, canonical, handover string) (func(), error) {
	rev, err := os.ReadFile(filepath.Join(filepath.Dir(PanelIn(staged)), "revision"))
	if err != nil {
		return nil, err
	}
	if r.expect != "" {
		rev = []byte(r.expect)
	}
	events := make(chan Event, 16)
	var k *Keeper
	k = &Keeper{
		URL: r.url, Bin: PanelIn(staged),
		Env:     helperEnvFor(r.newKind, r.addr),
		LogPath: filepath.Join(r.t.TempDir(), "new.log"), StartTimeout: 5 * time.Second,
		MinUptime: time.Minute, Poll: 100 * time.Millisecond,
		OnEvent: func(e Event) {
			// Where a panel came from, noted as it starts: the keeper starts
			// by path, and the path is what an update moves.
			if e.State == Starting {
				r.mu.Lock()
				r.newFrom = append(r.newFrom, k.bin())
				r.mu.Unlock()
			}
			events <- e
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	tk := &Takeover{
		URL: r.url, Handover: Handover{Path: handover}, Staged: staged, Canonical: canonical,
		Revision: strings.TrimSpace(string(rev)), Keeper: k, Events: events,
		StartKeeper: func() { go k.Run(ctx) },
		Registry:    r.registry,
		LockPath:    r.lockPath,
		Done: func() {
			r.mu.Lock()
			r.doneCalls++
			r.mu.Unlock()
		},
	}
	r.takeoverDone = done
	go func() {
		err := tk.Run(ctx)
		r.mu.Lock()
		r.takeover = err
		r.mu.Unlock()
		close(done)
	}()
	return func() { cancel(); <-done }, nil
}

// goBin is this machine's go, wherever it is. FindTools looks in the
// well-known places and then gives up, and a CI runner keeps go in none of
// them -- which is the very thing that made this source resolve its own tools
// (see TestATreeSourceSaysWhichToolItCannotFind). A test must not depend on
// the answer either way, so it hands over the path it was run with.
func goBin() string {
	path, _ := exec.LookPath("go")
	return path
}

func (r *updateRig) update(running string) *Update {
	makeBin, _ := exec.LookPath("make")
	return &Update{
		Source: &TreeSource{
			Dir:      r.f.tree,
			Remote:   "origin",
			Branch:   "master",
			Embedded: Tools{Git: r.f.git, Make: makeBin, Go: goBin()},
			Env:      append(r.f.env, "HELPER_BIN="+os.Args[0]),
			Running:  running,
		},
		Canonical:       r.canonical,
		LockPath:        r.lockPath,
		HandoverTimeout: 30 * time.Second,
		Launch:          r.launch,
		Pause:           r.pauseOld,
		Resume:          r.resumeOld,
		Progress: func(p Progress) {
			r.mu.Lock()
			r.progress = append(r.progress, p)
			r.mu.Unlock()
		},
	}
}

func (r *updateRig) steps() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var s []string
	for _, p := range r.progress {
		s = append(s, p.Step)
	}
	return s
}

func waitRevision(url, want string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if rev, err := panelRevision(context.Background(), url); err == nil && rev == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func revisionIn(t *testing.T, bundle string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(PanelIn(bundle)), "revision"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func TestAnUpdateBuildsToTheSideAndTheNewWindowPutsItInPlace(t *testing.T) {
	r := newUpdateRig(t)
	head := r.f.run(r.f.other, "rev-parse", "HEAD")

	// The invariant, watched for the whole update: something is at the
	// canonical path at every moment.
	var stop atomic.Bool
	var misses, reads atomic.Int64
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for !stop.Load() {
			if _, err := os.Stat(PanelIn(r.canonical)); err != nil {
				misses.Add(1)
			}
			reads.Add(1)
		}
	}()
	startsBefore := r.oldStarts.Load()
	err := r.update("old").Run(context.Background())
	stop.Store(true)
	<-watched
	if err != nil {
		t.Fatalf("update: %v (steps %v)", err, r.steps())
	}
	// The old window's keeper started nothing while the new window took over:
	// it was paused. Unpaused, it would start the old panel again the moment
	// the new window stopped it, and the two would race for the port.
	if n := r.oldStarts.Load() - startsBefore; n != 0 {
		t.Fatalf("the old window's keeper started %d panel(s) during the update", n)
	}
	if misses.Load() != 0 {
		t.Fatalf("%d of %d looks found nothing at the canonical path during the update", misses.Load(), reads.Load())
	}

	if got := revisionIn(t, r.canonical); got != head {
		t.Fatalf("the canonical bundle is build %q, want the tree's head %q", got, head)
	}
	if !waitRevision(r.url, head, 5*time.Second) {
		t.Fatal("the panel answering after the update is not the new build")
	}
	want := []string{"check", "build", "handover", "handover:alive", "handover:panel", "handover:swapped", "handover:done", "done"}
	if got := r.steps(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("steps %v, want %v", got, want)
	}
}

// One update starts two panels, and that is the whole of what a person sees
// as two restarts a moment apart (T-033, 2026-09-12). This pins which bundle
// each of the two came from, because that is the part the log does not say
// and the part that makes both starts necessary: the first cannot be the
// canonical bundle, which still holds the old version at that moment, and the
// second cannot be the staged one, which holds the version swapped out by
// then -- a keeper left pointing there would start the old build the next
// time its panel died. docs/engineering/window-and-panel.md says why at
// length.
func TestTheNewWindowStartsItsPanelTwiceStagedThenCanonical(t *testing.T) {
	r := newUpdateRig(t)
	staged := filepath.Join(StagingDir(r.canonical), "fleetdeck.app")

	if err := r.update("old").Run(context.Background()); err != nil {
		t.Fatalf("update: %v (steps %v)", err, r.steps())
	}

	from := r.startedFrom()
	want := []string{PanelIn(staged), PanelIn(r.canonical)}
	if strings.Join(from, ",") != strings.Join(want, ",") {
		t.Fatalf("the new window started panels from %v, want %v", from, want)
	}
}

// A new window whose panel will not start leaves everything as it was: the
// canonical bundle untouched, and the old panel answering again.
func TestAFailedHandoverLeavesTheOldBundleAndPanel(t *testing.T) {
	r := newUpdateRig(t)
	r.newKind = "crash"

	err := r.update("old").Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), crashNotice) {
		t.Fatalf("update: %v; want the new panel's own last words", err)
	}
	if got := revisionIn(t, r.canonical); got != "old" {
		t.Fatalf("the canonical bundle is %q after a failed handover, want the old one", got)
	}
	if !waitRevision(r.url, "old", 10*time.Second) {
		t.Fatal("the old panel does not answer again after the failed handover")
	}
	for _, s := range r.steps() {
		if s == "handover:swapped" || s == "done" {
			t.Fatalf("steps %v: a failed handover swapped or finished", r.steps())
		}
	}
}

// A new panel that answers, but with another build than the new window is,
// is not the proof the swap waits for: the canonical bundle stays, the staged
// panel is stopped, and the old one answers again.
func TestANewPanelOfAnotherBuildIsNotSwappedIn(t *testing.T) {
	r := newUpdateRig(t)
	r.expect = "a-build-that-was-not-built"

	err := r.update("old").Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "a-build-that-was-not-built") {
		t.Fatalf("update: %v; want the mismatch named", err)
	}
	if got := revisionIn(t, r.canonical); got != "old" {
		t.Fatalf("the canonical bundle is %q, want the old one", got)
	}
	if !waitRevision(r.url, "old", 10*time.Second) {
		t.Fatal("the old panel does not answer again: the staged one was left holding the port")
	}
}

func TestAnUpdateWithNothingNewSaysSoAndBuildsNothing(t *testing.T) {
	r := newUpdateRig(t)
	head := r.f.run(r.f.other, "rev-parse", "HEAD")
	if _, err := r.f.supervisedTree().Forward(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := r.update(head).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.steps(), ","); got != "check,current" {
		t.Fatalf("steps %s, want check,current", got)
	}
	if _, err := os.Stat(StagingDir(r.canonical)); !os.IsNotExist(err) {
		t.Fatalf("something was built for an update with nothing new: %v", err)
	}
}

func TestASecondPressWhileAnUpdateRunsIsRefused(t *testing.T) {
	r := newUpdateRig(t)
	u := r.update("old")
	release, err := Acquire(u.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := u.Run(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("update while another runs: %v, want ErrBusy", err)
	}
	if len(r.steps()) != 0 {
		t.Fatalf("a refused update went on: %v", r.steps())
	}
}

// On 2026-09-14 the app opened from its name was the version an update had
// just replaced: the new window had started from the staged path, macOS had
// kept that path for the app's identifier, and after the swap the staged path
// held the old bundle. So once the swap is done the new window has
// LaunchServices forget the staged path and take the canonical one -- before
// it says done, while the old window can still be told if anything fails.
func TestTheNewWindowHasLaunchServicesForgetTheBundleSwappedOut(t *testing.T) {
	r := newUpdateRig(t)
	if err := r.update("old").Run(context.Background()); err != nil {
		t.Fatalf("update: %v (steps %v)", err, r.steps())
	}
	<-r.takeoverDone
	staged := filepath.Join(StagingDir(r.canonical), BundleName)
	want := []registryCall{
		{op: "forget", bundle: staged, steps: "alive,panel,swapped"},
		{op: "register", bundle: r.canonical, steps: "alive,panel,swapped"},
	}
	got := r.registry.told()
	if len(got) != len(want) {
		t.Fatalf("LaunchServices was told %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("LaunchServices was told %+v, want %+v", got, want)
		}
	}
}

// The bundle swapped out is the old version under the app's own identifier,
// and anything that walks LaunchServices again finds it. It does not outlive
// a successful update: the new window removes it -- once the old window's
// update has let go of the lock, since until then that window runs out of it.
// Done is said first: the window opens the panel's page on it, and has no
// reason to wait for the tidying.
func TestTheBundleSwappedOutIsRemovedOnceTheOldWindowLetsGoOfTheLock(t *testing.T) {
	r := newUpdateRig(t)
	head := r.f.run(r.f.other, "rev-parse", "HEAD")
	u := r.update("old")
	// A lock of the test's own for the new window to wait on, held past the
	// update's end.
	r.lockPath = filepath.Join(t.TempDir(), "held.lock")
	release, err := Acquire(r.lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Run(context.Background()); err != nil {
		release()
		t.Fatalf("update: %v (steps %v)", err, r.steps())
	}
	staged := filepath.Join(StagingDir(r.canonical), BundleName)
	time.Sleep(300 * time.Millisecond)
	r.mu.Lock()
	done := r.doneCalls
	r.mu.Unlock()
	if done != 1 {
		release()
		t.Fatalf("Done was called %d times by the time the update ended, want once", done)
	}
	if _, err := os.Stat(staged); err != nil {
		release()
		t.Fatalf("the bundle swapped out went while the lock was held: %v", err)
	}
	release()
	select {
	case <-r.takeoverDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the new window's takeover did not end once the lock was free")
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("the bundle swapped out is still at %s after a successful update: %v", staged, err)
	}
	if got := revisionIn(t, r.canonical); got != head {
		t.Fatalf("the canonical bundle is %q, want %q", got, head)
	}
	// What says how the handover went stays, for whoever looks next.
	if _, err := os.Stat(filepath.Join(StagingDir(r.canonical), "handover")); err != nil {
		t.Fatalf("the handover file went with the bundle: %v", err)
	}
}

// A failed handover leaves the canonical bundle as it was, and the old window
// says so -- with where to read why: the new window's own log.
func TestAFailedHandoverNamesTheNewWindowsLogAndTouchesNoRegistration(t *testing.T) {
	r := newUpdateRig(t)
	r.newKind = "crash"
	err := r.update("old").Run(context.Background())
	logPath := filepath.Join(StagingDir(r.canonical), NewWindowLog)
	if err == nil || !strings.Contains(err.Error(), logPath) {
		t.Fatalf("update: %v; want the new window's log %s named", err, logPath)
	}
	if got := r.registry.told(); len(got) != 0 {
		t.Fatalf("a failed handover told LaunchServices %+v", got)
	}
}
