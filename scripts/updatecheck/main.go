package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

const dittoPath = "/usr/bin/ditto"

// oldPanelStartTimeout is how long v0.10.0's panel has to answer when this
// program first starts it. Not v0.10.0's own 330 ms: on the operator's machine
// that panel has been up for hours when the button is pressed, and a cold
// runner's first start is not what is being checked. The handover does not
// use it -- the keeper is paused before the new window starts.
const oldPanelStartTimeout = 30 * time.Second

type options struct {
	oldArchive, newArchive string
	canonical              string
	url                    string
	wantRevision, wantOld  string
	tagWindow              string
	launchServices         bool
	out                    string
}

// How LaunchServices is looked at after the handover:
//
//   - windowReadyWait: for the new window to say the handover is done, which it
//     does once its window is made and running -- its check-in with
//     LaunchServices, and its telling LaunchServices again where the app is,
//     behind it (cmd/fleetdeck-window/handoverstart.go);
//   - observeLooks: the looks taken while this program, the old window, still
//     runs and the bundle swapped out is still on disk. They only observe:
//     LaunchServices registers that bundle's path again about half a second
//     after it is told to forget it (run 34867562033);
//   - retireWait: for the new window to remove the bundle swapped out, which it
//     does within a look at the update lock once its parent has gone;
//   - gateLooks and gateSpacing: the looks that must all pass once this program
//     has gone, each started no sooner than gateSpacing after the one before.
//     One `lsregister -dump` took 8 s on a macos-26 runner, so ten take 80-90 s.
const (
	windowReadyWait = 30 * time.Second
	observeLooks    = 3
	retireWait      = 30 * time.Second
	gateLooks       = 10
	gateSpacing     = 6 * time.Second
)

// The bundle identifiers asked about: this build's, installed at the canonical
// path on the runner under the stand identifier, and v0.10.0's, the version
// swapped out. On a person's machine the two are one identifier.
const (
	installedBundleID = supervisor.StandBundleID
	replacedBundleID  = "dev.fleetdeck.window"
)

// appURLScript asks NSWorkspace what a bundle identifier opens. It reads
// LaunchServices and nothing more: no AppleEvent is sent and nothing is
// started. The identifier arrives in argv, never in the script's text.
const appURLScript = `ObjC.import("AppKit"); function run(a){var u=$.NSWorkspace.sharedWorkspace.URLForApplicationWithBundleIdentifier(a[0]); return u.isNil()?"":u.path.js}`

// windowDoneLine is what the new window logs once the handover is done and its
// window runs.
const windowDoneLine = "fleetdeck-window: the handover is done"

// waitForWindowDone waits for the new window to say the handover is done.
func waitForWindowDone(canonical string) error {
	windowLog := filepath.Join(supervisor.StagingDir(canonical), supervisor.NewWindowLog)
	if err := waitFor("the new window saying the handover is done", windowReadyWait, 100*time.Millisecond, func() (bool, error) {
		data, err := os.ReadFile(windowLog)
		return err == nil && strings.Contains(string(data), windowDoneLine), err
	}); err != nil {
		return err
	}
	log.Printf("updatecheck: the new window says the handover is done")
	return nil
}

// appURL is the path of what id opens, "" when it opens nothing.
func appURL(id string) (string, error) {
	out, err := exec.Command("/usr/bin/osascript", "-l", "JavaScript", "-e", appURLScript, id).Output()
	if err != nil {
		return "", fmt.Errorf("ask what %s opens: %w", id, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// lsLook is one look at LaunchServices: what each identifier opens, then, from
// one moment on, the apps on disk in the staging directory and the dump taken
// straight after, with the dump's staged records told apart by that moment.
type lsLook struct {
	n                   int
	installed, replaced string
	at                  time.Time
	onDisk              map[string]bool
	dump                string
	existing, gone      []string
}

func takeLook(canonical string, n int) (lsLook, error) {
	l := lsLook{n: n}
	var err error
	if l.installed, err = appURL(installedBundleID); err != nil {
		return l, err
	}
	if l.replaced, err = appURL(replacedBundleID); err != nil {
		return l, err
	}
	l.at = time.Now()
	l.onDisk = appsIn(supervisor.StagingDir(canonical))
	data, err := exec.Command(supervisor.LsregisterPath, "-dump").Output()
	if err != nil {
		return l, fmt.Errorf("lsregister -dump: %w", err)
	}
	l.dump = string(data)
	l.existing, l.gone = stagedRecords(l.dump, canonical, func(p string) bool { return l.onDisk[p] })
	return l, nil
}

func (l lsLook) print(phase string) {
	onDisk := make([]string, 0, len(l.onDisk))
	for p := range l.onDisk {
		onDisk = append(onDisk, p)
	}
	sort.Strings(onDisk)
	log.Printf("updatecheck: LaunchServices, %s, look %d: dump started %s; apps on disk in the staging directory then: %v; %s opens %q; %s opens %q",
		phase, l.n, l.at.Format("15:04:05.000"), onDisk, installedBundleID, l.installed, replacedBundleID, l.replaced)
	for _, line := range fleetdeckPaths(l.dump) {
		log.Printf("updatecheck: LaunchServices, %s, look %d: %s", phase, l.n, line)
	}
	for _, p := range l.gone {
		log.Printf("updatecheck: LaunchServices, %s, look %d: observed: a record of %s, which was not on disk as the dump started", phase, l.n, p)
	}
}

// gate is what must hold of a look once the old window has gone: nothing
// LaunchServices can open in the staging directory, by path or by identifier.
func (l lsLook) gate(canonical string) error {
	if len(l.existing) > 0 {
		return fmt.Errorf("LaunchServices holds %s, on disk in the staging directory as the dump started", strings.Join(l.existing, ", "))
	}
	if err := resolvesTo(l.installed, canonical, true); err != nil {
		return err
	}
	return resolvesTo(l.replaced, canonical, false)
}

// keepDump keeps a look's dump in out as lsregister-<name>.txt.
func keepDump(out, name string, l lsLook) {
	if out == "" || l.dump == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(out, "lsregister-"+name+".txt"), []byte(l.dump), 0o644); err != nil {
		log.Printf("updatecheck: keep lsregister-%s.txt: %v", name, err)
	}
}

// observeLaunchServices looks at LaunchServices while this program -- the old
// window -- still runs, and so the bundle swapped out is still on disk. It
// says what it sees and requires nothing: on the operator's machine this
// moment lasts a fraction of a second, and LaunchServices registers that
// bundle's path again on its own while the bundle is there.
func observeLaunchServices(canonical, out string) {
	var last lsLook
	for n := 1; n <= observeLooks; n++ {
		l, err := takeLook(canonical, n)
		if err != nil {
			log.Printf("updatecheck: LaunchServices, before-retire, look %d: %v", n, err)
			continue
		}
		l.print("before-retire")
		if n == 1 {
			keepDump(out, "before-retire", l)
		}
		last = l
	}
	if last.n > 1 {
		keepDump(out, "before-retire-last", last)
	}
}

// checkLaunchServices is what an update leaves behind once the old window has
// gone, as this program's update run has when this runs: the bundle swapped
// out removed, and, in every one of gateLooks looks, nothing LaunchServices
// can open in the staging directory.
func checkLaunchServices(o options) error {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return errors.New("runs on a GitHub Actions runner only: it checks an app this program installed")
	}
	if err := waitForWindowDone(o.canonical); err != nil {
		return err
	}
	swappedOut := filepath.Join(supervisor.StagingDir(o.canonical), supervisor.BundleName)
	if err := waitFor("the new window removing the bundle swapped out at "+swappedOut, retireWait, 100*time.Millisecond, func() (bool, error) {
		_, err := os.Lstat(swappedOut)
		return errors.Is(err, os.ErrNotExist), nil
	}); err != nil {
		return err
	}
	log.Printf("updatecheck: pass: the new window removed the bundle swapped out at %s", swappedOut)

	for n := 1; n <= gateLooks; n++ {
		started := time.Now()
		l, err := takeLook(o.canonical, n)
		if err != nil {
			return err
		}
		l.print("after-retire")
		if n == 1 {
			keepDump(o.out, "after-retire", l)
		}
		if err := l.gate(o.canonical); err != nil {
			keepDump(o.out, "after-retire-last", l)
			return fmt.Errorf("after-retire, look %d: %w", n, err)
		}
		if n == gateLooks {
			keepDump(o.out, "after-retire-last", l)
			break
		}
		if wait := gateSpacing - time.Since(started); wait > 0 {
			time.Sleep(wait)
		}
	}
	log.Printf("updatecheck: pass: in %d looks, %s opened %s, %s opened nothing in %s, and LaunchServices held nothing on disk there",
		gateLooks, installedBundleID, o.canonical, replacedBundleID, supervisor.StagingDir(o.canonical))
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	var o options
	flag.StringVar(&o.oldArchive, "old-archive", "", "the v0.10.0 release zip, as published")
	flag.StringVar(&o.newArchive, "new-archive", "", "this branch's app zip")
	flag.StringVar(&o.canonical, "canonical", "/Applications/fleetdeck.app", "where the installed app goes; must not exist")
	flag.StringVar(&o.url, "url", "", "the URL the stand's panel answers on, the port its configuration names")
	flag.StringVar(&o.wantRevision, "want-revision", "", "the commit this branch's app was built from")
	flag.StringVar(&o.wantOld, "want-old-version", "v0.10.0", "the version the installed app reports")
	flag.StringVar(&o.tagWindow, "tag-window", "", "cmd/fleetdeck-window of the v0.10.0 checkout this program was built in")
	flag.BoolVar(&o.launchServices, "launchservices", false, "once an update run of this program has gone: check the bundle swapped out is removed and nothing opens it")
	flag.StringVar(&o.out, "out", "", "a directory to keep the LaunchServices dumps in")
	flag.Parse()
	check := run
	if o.launchServices {
		check = checkLaunchServices
	}
	if err := check(o); err != nil {
		log.Printf("updatecheck: FAILED: %v", err)
		os.Exit(1)
	}
}

func run(o options) error {
	if o.oldArchive == "" || o.newArchive == "" || o.url == "" || o.wantRevision == "" || o.tagWindow == "" {
		return errors.New("-old-archive, -new-archive, -url, -want-revision and -tag-window are all needed")
	}
	// It installs an app into /Applications and starts windows: a runner's
	// business, never a person's machine.
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return errors.New("runs on a GitHub Actions runner only: it installs an app and opens windows")
	}
	standSocket := os.Getenv("FLEETDECK_STAND_SOCKET")
	if strings.TrimSpace(standSocket) == "" {
		return errors.New("FLEETDECK_STAND_SOCKET is not set: a panel started without it would look for a fleet daemon")
	}
	sources, err := readTagWindow(o.tagWindow)
	if err != nil {
		return err
	}
	if err := copiedFrom(sources); err != nil {
		return err
	}
	staging := supervisor.StagingDir(o.canonical)
	for _, p := range []string{o.canonical, staging} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s is already there; this program installs into an empty place only", p)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	if err := install(o.oldArchive, o.canonical); err != nil {
		return err
	}
	if v, err := versionOf(o.canonical); err != nil || v != o.wantOld {
		return fmt.Errorf("the installed app reports %q (%v), want %s", v, err, o.wantOld)
	}

	// v0.10.0's window's keeper, on the installed panel.
	keeper := &supervisor.Keeper{
		URL:          o.url,
		Bin:          supervisor.PanelIn(o.canonical),
		Args:         panelArgs(os.Getpid(), standSocket),
		Owner:        os.Getpid(),
		Env:          os.Environ(),
		LogPath:      filepath.Join(home, "Library", "Logs", "fleetdeck.log"),
		StartTimeout: oldPanelStartTimeout,
		MinUptime:    launchdThrottle,
		Poll:         takenPanelPoll,
		OnEvent: func(e supervisor.Event) {
			log.Printf("updatecheck: v0.10.0's keeper: %s pid %d %v", e.State, e.PID, e.Err)
		},
	}
	kept := &keeperRun{k: keeper}
	kept.start()
	if b, err := waitPanel(o.url, 60*time.Second); err != nil || b.Version != o.wantOld {
		return fmt.Errorf("v0.10.0's panel did not answer at %s as %s: %+v, %v", o.url, o.wantOld, b, err)
	}
	log.Printf("updatecheck: v0.10.0 is installed at %s and its panel answers at %s", o.canonical, o.url)

	// The press, minus the press.
	lockPath, err := supervisor.LockPath(o.canonical)
	if err != nil {
		return err
	}
	var (
		mu      sync.Mutex
		steps   []stepAt
		started time.Time
	)
	u := &supervisor.Update{
		Source:          &archiveSource{archive: o.newArchive, revision: o.wantRevision},
		Canonical:       o.canonical,
		LockPath:        lockPath,
		HandoverTimeout: handoverTimeout,
		Launch: launchNewWindow(o.url, func() {
			mu.Lock()
			defer mu.Unlock()
			started = time.Now()
		}),
		Pause:  kept.stop,
		Resume: kept.start,
		Progress: func(p supervisor.Progress) {
			mu.Lock()
			defer mu.Unlock()
			steps = append(steps, stepAt{p.Step, time.Now()})
			log.Printf("updatecheck: update %s %s", p.Step, p.Detail)
		},
	}
	runErr := u.Run(context.Background())
	mu.Lock()
	took := time.Since(started)
	timed := append([]stepAt(nil), steps...)
	mu.Unlock()
	windowLog := dump(staging)
	log.Printf("updatecheck: from the new window's start, against v0.10.0's %s:\n  %s", handoverTimeout, strings.Join(timeline(started, windowLog, timed), "\n  "))
	if runErr != nil {
		return fmt.Errorf("the update from v0.10.0 failed %s after the new window was started, against v0.10.0's %s: %w", took.Round(time.Millisecond), handoverTimeout, runErr)
	}
	got := make([]string, 0, len(timed))
	for _, s := range timed {
		got = append(got, s.step)
	}
	if err := handoverInOrder(got); err != nil {
		return err
	}
	log.Printf("updatecheck: pass: handover alive, panel, swapped, done, %s after the new window was started, within v0.10.0's %s", took.Round(time.Millisecond), handoverTimeout)

	b, err := panelBuild(context.Background(), o.url)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(o.canonical)
	if err != nil {
		return err
	}
	if b.Revision != o.wantRevision || !strings.HasPrefix(b.Executable, canonical+string(filepath.Separator)) {
		return fmt.Errorf("the panel at %s is %s from %s, want %s from inside %s", o.url, b.Revision, b.Executable, o.wantRevision, canonical)
	}
	log.Printf("updatecheck: pass: the panel at %s is this branch's %s, from %s", o.url, b.Revision, b.Executable)

	swappedOut := filepath.Join(staging, supervisor.BundleName)
	if v, err := versionOf(swappedOut); err != nil || v != o.wantOld {
		return fmt.Errorf("the bundle swapped out at %s reports %q (%v), want %s", swappedOut, v, err, o.wantOld)
	}
	log.Printf("updatecheck: pass: %s swapped out is at %s", o.wantOld, swappedOut)

	// While this program -- the old window -- still runs, the new window does
	// not remove the bundle swapped out: LaunchServices is observed then, once
	// the new window's check-in is behind it.
	if err := waitForWindowDone(o.canonical); err != nil {
		return err
	}
	observeLaunchServices(o.canonical, o.out)
	return nil
}

// install unpacks the release archive and puts its app at canonical.
func install(archive, canonical string) error {
	dir, err := os.MkdirTemp("", "updatecheck-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if out, err := exec.Command(dittoPath, "-x", "-k", archive, dir).CombinedOutput(); err != nil {
		return fmt.Errorf("unpack %s: %w\n%s", archive, err, out)
	}
	if out, err := exec.Command(dittoPath, filepath.Join(dir, supervisor.BundleName), canonical).CombinedOutput(); err != nil {
		return fmt.Errorf("install %s: %w\n%s", canonical, err, out)
	}
	return nil
}

// archiveSource is this branch's app, from a local archive, in place of
// v0.10.0's ReleaseSource: no download and no Seal.Verify.
type archiveSource struct {
	archive, revision string
}

func (s *archiveSource) Check(_ context.Context) (string, error) { return s.revision, nil }

// Stage unpacks the archive into dir and puts its bundle beside the installed
// app, where ReleaseSource.Stage puts the bundle it has checked.
func (s *archiveSource) Stage(ctx context.Context, dir, _ string, say func(supervisor.Progress)) (string, error) {
	if say != nil {
		say(supervisor.Progress{Step: "unpack", Detail: s.archive})
	}
	unpacked := filepath.Join(dir, "unpacked")
	defer func() { _ = os.RemoveAll(unpacked) }()
	if out, err := exec.CommandContext(ctx, dittoPath, "-x", "-k", s.archive, unpacked).CombinedOutput(); err != nil {
		return "", fmt.Errorf("unpack %s: %w\n%s", s.archive, err, out)
	}
	final := filepath.Join(dir, supervisor.BundleName)
	if err := os.Rename(filepath.Join(unpacked, supervisor.BundleName), final); err != nil {
		return "", err
	}
	return final, nil
}

// launchNewWindow is v0.10.0's (cmd/fleetdeck-window/update.go of the tag),
// with one thing v0.10.0's has not: onStart marks the moment the new window's
// process has started, for the timeline.
func launchNewWindow(url string, onStart func()) func(staged, canonical, handover string) (func(), error) {
	return func(staged, canonical, handover string) (func(), error) {
		logPath := filepath.Join(filepath.Dir(handover), supervisor.NewWindowLog)
		out, err := os.Create(logPath)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(filepath.Join(staged, "Contents", "MacOS", "fleetdeck-window"), newWindowArgs(url, handover, canonical)...)
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			_ = out.Close()
			return nil, err
		}
		onStart()
		log.Printf("updatecheck: the new window started (pid %d)", cmd.Process.Pid)
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			_ = out.Close()
			close(done)
		}()
		return func() {
			_ = cmd.Process.Kill()
			<-done
		}, nil
	}
}

// keeperRun is v0.10.0's (cmd/fleetdeck-window/main.go of the tag).
type keeperRun struct {
	k      *supervisor.Keeper
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *keeperRun) start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.cancel, r.done = cancel, done
	go func() {
		r.k.Run(ctx)
		close(done)
	}()
}

func (r *keeperRun) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.cancel = nil
}

type build struct {
	Version    string `json:"version"`
	Revision   string `json:"revision"`
	Executable string `json:"executable"`
}

// panelBuild is what the panel at url says of its build.
func panelBuild(ctx context.Context, url string) (build, error) {
	u, err := neturl.Parse(url)
	if err != nil {
		return build{}, err
	}
	u.Path, u.RawQuery = "/api/snapshot", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return build{}, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return build{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var snap struct {
		Build *build `json:"build"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&snap); err != nil || snap.Build == nil {
		return build{}, fmt.Errorf("the snapshot at %s carries no build (%s)", u, resp.Status)
	}
	return *snap.Build, nil
}

func waitPanel(url string, within time.Duration) (build, error) {
	deadline := time.Now().Add(within)
	for {
		b, err := panelBuild(context.Background(), url)
		if err == nil || time.Now().After(deadline) {
			return b, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// versionOf is what the panel binary in bundle says its version is.
func versionOf(bundle string) (string, error) {
	out, err := exec.Command(supervisor.PanelIn(bundle), "version").CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// dump prints what the new window left beside the handover -- the handover
// file and its log -- and returns the log.
func dump(staging string) string {
	var windowLog string
	for _, name := range []string{"handover", supervisor.NewWindowLog} {
		data, err := os.ReadFile(filepath.Join(staging, name))
		if err != nil {
			log.Printf("updatecheck: %s: %v", name, err)
			continue
		}
		log.Printf("updatecheck: --- %s\n%s", name, data)
		if name == supervisor.NewWindowLog {
			windowLog = string(data)
		}
	}
	return windowLog
}
