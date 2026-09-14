//go:build darwin

// Command fleetdeck-window is the fleetdeck app: a native window around the
// panel, and the panel's owner.
//
// Owner is new. Until 2026-09-11 this command owned only itself: the panel ran
// as a separate process, started at login by a launch agent `fleetdeck init`
// installed, and the window never started, stopped or restarted it -- it only
// opened a window on the URL the panel already answered on, the way a browser
// tab does. The operator revoked that contract on 2026-09-11: the app is to
// need no terminal and no ritual. So the app bundle carries the panel, beside
// this binary in Contents/MacOS, and the window starts it. What the window
// owns now:
//
//   - starting the panel when nothing answers at its URL, and starting it
//     again when it dies, the way launchd's KeepAlive did for the agent -- and,
//     like launchd, not over and over when it dies at once (see
//     internal/supervisor's Keeper);
//   - saying so in the window, with the end of the panel's log, when the panel
//     will not start, and starting it again when asked.
//
// The panel lives exactly as long as the window -- the operator's rule, later
// the same day, 2026-09-11: "with fleetdeck the panel goes out, with its start
// it starts". The first version of this command let the panel outlive the
// window, and a panel an earlier window left behind was how a new window came
// to show an old build. Now the window starts its panel with its own PID as
// the owner, and the panel goes when the window's process does, however it
// went: quit, crash, SIGKILL (cmd/fleetdeck, owner.go). Hiding the window --
// the red button -- is not the window going: the process runs on, and so does
// the panel. A panel a window left behind that still answers is replaced; a
// panel started from a terminal, or anything that is not a fleetdeck panel,
// is used as it is (see internal/supervisor's Keeper) -- and, when it is not
// this window's build, named over its page (foreign.go).
//
// Why webview_go, and what the fallback is: this needed a native window
// without a second build toolchain in a project that currently has only Go.
// webview_go wraps WKWebView on macOS via cgo and nothing else — no bundled
// browser runtime, no separate frontend dependency chain. A probe binary
// built from this exact dependency comes to 2.9MB, linked only against
// system frameworks (WebKit, CoreFoundation, libSystem, libresolv, libc++,
// libobjc — see TestWindowBinaryLinksAgainstWebKit), against roughly 3-10MB
// for the same window built with Tauri and ~89MB for Electron's minimal
// macOS bundle. The one real cost: webview_go carries no tagged releases and
// its last commit predates this file by more than a year, so the dependency
// below is pinned to an exact commit, not a branch or a floating version.
// If it stops working, the fallback is Tauri (the second-cheapest path, and
// the one with an actively maintained toolchain) — and because this wrapper
// is deliberately thin -- the panel's keeping lives in internal/supervisor,
// with no cgo -- that rewrite touches this one command, never the panel.
//
// The //go:build darwin above is load-bearing, not decoration: this project's
// own CI runs a ubuntu-latest leg too, and webview_go's cgo directives ask
// for gtk+-3.0 and webkit2gtk-4.0 via pkg-config on Linux -- packages that
// CI runner does not have installed, and this project has no reason to
// install, since the panel itself is darwin-only (spec section 1). Without
// the tag, `go build ./...`/`govulncheck ./...` on that runner tried to
// compile this package anyway and failed on the missing pkg-config entries;
// with it, the package has no buildable files at all outside darwin, and the
// standard toolchain already treats that as "nothing to build here", not an
// error -- confirmed with GOOS=linux locally, not assumed.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	webview "github.com/webview/webview_go"

	"github.com/kroticw/fleetdeck/internal/supervisor"
	"github.com/kroticw/fleetdeck/internal/version"
)

// defaultURL matches cmd/fleetdeck-status's own default (see its FLEETDECK_ENDPOINT
// handling) rather than reading internal/config: the window has exactly one thing to
// know -- where the panel answers -- and pulling in the whole config package to learn
// a port would be a second way to fail the config file can never affect the panel
// itself failing to load.
const defaultURL = "http://127.0.0.1:7777/"

// reloadBindingName is the global function the window gives the page. The page
// (web/js/buildcheck.js) calls it when the panel under it has been replaced by
// one serving a different interface, and the window navigates it afresh -- no
// Cmd+Q, no person. Its presence is also how the page knows it is inside this
// window: in a browser nothing binds it, and the page asks for a click instead.
// The name is a contract across two languages; reload_test.go checks both
// sides still spell it the same way.
const reloadBindingName = "fleetdeckReload"

// reloadBinding is what the page's call runs. It hops to the UI thread before
// touching the web view -- a bound function is called from the web view's own
// callback, and the web view is only safe to drive from the UI thread -- and
// navigates to the panel's URL rather than reloading: a reload may take the
// document from cache, a navigation asks the server.
func reloadBinding(dispatch func(func()), navigate func(string), url string) func() {
	return func() {
		dispatch(func() { navigate(url) })
	}
}

func main() {
	url := flag.String("url", defaultURL, "URL the panel answers on")
	handover := flag.String("handover", "", "set by an update: the handover file of the window taking the panel over")
	toldCanonical := flag.String("canonical", "", "set by an update: the installed app bundle this window replaces")
	flag.Parse()
	// For a window started by an update, the window that started it.
	oldWindow := os.Getppid()

	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("fleetdeck-window: locate own binary: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("fleetdeck-window: locate home directory: %v", err)
	}
	// The same log the launch agent wrote the panel's output to, so a panel's
	// history does not split in two at the day the window took over.
	logPath := filepath.Join(home, "Library", "Logs", "fleetdeck.log")

	// A stand's socket, when this window runs on a stand, handed to every panel
	// it starts; a stand that names none is refused before anything starts.
	standSocket, err := standIsolation(os.LookupEnv)
	if err != nil {
		log.Fatalf("fleetdeck-window: %v", err)
	}

	// A window in an update's staging directory opens the installed app and
	// goes, before it has a window to flash (staged.go).
	where := whereToRun(exe, *handover, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
	if where.action == runElsewhere {
		log.Printf("fleetdeck-window: started from %s, in an update's staging directory; opening the installed app at %s instead", where.staged, where.installed)
		if where.err = goToInstalled(where); where.err == nil {
			return
		}
		log.Printf("fleetdeck-window: %v", where.err)
		where.action = runRefused
	}

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("fleetdeck")
	w.SetSize(1440, 900, webview.HintNone)

	// Both calls need the native window, which is already valid here --
	// confirmed by timing, not assumed: SetTitle/SetSize above already rely
	// on the same fact. installMenu is what makes Cmd+X/C/V/A/Z (and Cmd+Q)
	// do anything at all; see menu_darwin.c for why. installCloseToHide
	// makes the red button hide the window rather than quit: the process runs
	// on, and with it the panel it owns -- hiding is not the window going.
	installMenu()
	installCloseToHide(w.Window())

	if where.action == runRefused {
		log.Printf("fleetdeck-window: started from %s, in an update's staging directory, with no installed app to open at %s; starting no panel", where.staged, where.installed)
		w.SetHtml(stagedPage(where))
		w.Run()
		return
	}

	scr := &screen{url: *url, logPath: logPath, takingOver: *handover != "", now: time.Now}
	// show does what the screen says, on the UI thread.
	show := func(navigate bool, page string) {
		switch {
		case navigate:
			log.Printf("fleetdeck-window: asked for the panel's page at %s (try %d)", scr.target(), scr.tries)
			w.Navigate(scr.target())
		case page != "":
			w.SetHtml(page)
		}
	}
	keeper := &supervisor.Keeper{
		URL:  *url,
		Bin:  panelBinary(exe),
		Args: panelArgs(os.Getpid(), standSocket),
		// This window: its panels report it as their owner and go when it
		// goes, and a panel whose window is gone is replaced.
		Owner:        os.Getpid(),
		Env:          os.Environ(),
		LogPath:      logPath,
		StartTimeout: panelStartTimeout,
		MinUptime:    launchdThrottle,
		Poll:         takenPanelPoll,
	}
	// A panel this window did not start, of another build than the window's,
	// is used as it is and named over its page (foreign.go).
	own := ownBuild()
	notices := &panelNotice{}
	// Asked again at a press, about the panel on the port by then.
	keeper.MayReplace = mayReplace(own, *url, home)
	// A takeover watches the keeper's events too, while it runs.
	var takeoverEvents atomic.Pointer[chan supervisor.Event]
	keeper.OnEvent = func(e supervisor.Event) {
		log.Printf("fleetdeck-window: panel %s", describeEvent(e))
		if ch := takeoverEvents.Load(); ch != nil {
			select {
			case *ch <- e:
			default:
			}
		}
		// Worked out here, off the UI thread: it may read a launch agent's file.
		n := noticeFor(own, *url, e, home)
		w.Dispatch(func() {
			if notices.set(n) {
				if n != nil {
					log.Printf("fleetdeck-window: %s", n)
				} else {
					log.Printf("fleetdeck-window: the notice about the panel at %s is taken down: %s", *url, describeEvent(e))
				}
				w.Eval("window." + noticeRepaintFunction + " && window." + noticeRepaintFunction + "()")
			}
			show(scr.on(e))
		})
	}
	kept := &keeperRun{k: keeper}

	// Bound before the first navigation, so the page finds them from its very
	// first load. A reload the page or a person asks for is a navigation the
	// screen counts, like the keeper's.
	// The page it was on, not the window's own URL: a reload from a fleet's
	// page stays on that fleet.
	if err := w.Bind(reloadBindingName, reloadBinding(w.Dispatch, func(string) {
		scr.reopen()
		log.Printf("fleetdeck-window: asked for the panel's page again at %s, as the page asked", scr.target())
		w.Navigate(scr.target())
	}, *url)); err != nil {
		log.Printf("fleetdeck-window: the page will not be able to reload itself: %v", err)
	}
	if err := w.Bind(pageLoadedBindingName, func(state, href string) {
		w.Dispatch(func() {
			if scr.asked {
				log.Printf("fleetdeck-window: the panel's page says %q at %s, %s after it was asked for", state, href, time.Since(scr.askedAt).Round(time.Millisecond))
			} else {
				log.Printf("fleetdeck-window: the panel's page says %q at %s", state, href)
			}
			scr.pageSays(state, href)
		})
	}); err != nil {
		log.Printf("fleetdeck-window: the window will not know whether the panel's page loaded: %v", err)
	}
	if err := w.Bind(startBindingName, keeper.Retry); err != nil {
		log.Printf("fleetdeck-window: the failure page will not be able to start the panel again: %v", err)
	}
	if err := w.Bind(chooseFolderBindingName, chooseFolder); err != nil {
		log.Printf("fleetdeck-window: the setup page will offer no folder chooser: %v", err)
	}
	if err := w.Bind(noticeBindingName, notices.page); err != nil {
		log.Printf("fleetdeck-window: a panel of another build will be shown without a word: %v", err)
	}
	if err := w.Bind(noticeShownBindingName, func(text string) {
		if text == "" {
			log.Printf("fleetdeck-window: the page took the notice about the panel down")
			return
		}
		log.Printf("fleetdeck-window: the page shows the notice about the panel: %q", text)
	}); err != nil {
		log.Printf("fleetdeck-window: the log will not say whether the notice reached the page: %v", err)
	}
	// The press names the panel it was shown -- by the PID on the port when the
	// notice was drawn -- and is taken only for the notice shown now. The page
	// belongs to the panel, and its own script can call this as well as the
	// button can; the keeper checks the port again before anything is stopped.
	if err := w.Bind(replaceBindingName, func(pid int) {
		want, ok := replaceRequest(notices.current(), pid)
		if !ok {
			log.Printf("fleetdeck-window: a press to replace the panel at %s (pid %d) is not for the notice shown now; ignored", *url, pid)
			return
		}
		log.Printf("fleetdeck-window: replacing the panel at %s (pid %d), as asked from the notice", *url, pid)
		keeper.Replace(want)
	}); err != nil {
		log.Printf("fleetdeck-window: the notice's button will not replace the panel: %v", err)
	}
	// After the bindings, so the script finds them; before the first
	// navigation, so it runs in the first page too.
	w.Init(noticeScript)
	w.Init(pageLoadScript(*url))
	// The update button is on screen only while there is something to update
	// to, and its appearing is the notice (watch.go). A build that cannot
	// update itself never finds anything to update to, so it shows no button;
	// why it cannot is in the log, and in the answer to the update binding
	// should anything call it anyway.
	canonical := canonicalBundle(exe, *toldCanonical)
	how := updateWay(config{
		tree:      treeDir,
		exe:       exe,
		version:   version.String(),
		teamID:    ownTeamID(exe),
		canonical: *toldCanonical,
	})
	if how.Refusal != "" {
		log.Printf("fleetdeck-window: this build cannot update itself: %s", how.Refusal)
	}
	var watch *updateWatch
	if how.Source != nil {
		if markPath, err := askedMarkPath(); err != nil {
			log.Printf("fleetdeck-window: no home directory to keep the answer about a newer version in, so this window will not look for one: %v", err)
		} else {
			watch = &updateWatch{
				source:   how.Source,
				running:  how.Running,
				markPath: markPath,
				tell:     func(r report) { tell(w, r) },
				now:      time.Now,
			}
		}
	}
	// A page asks this as it loads -- the first time, and again after every
	// reload -- because it misses every report sent before it was there.
	if err := w.Bind(knownBindingName, func() report {
		if watch == nil {
			return report{Step: "none"}
		}
		return watch.known()
	}); err != nil {
		log.Printf("fleetdeck-window: a page that loads will not learn of a newer version until the window next finds one: %v", err)
	}
	var updating atomic.Bool
	if err := w.Bind(updateBindingName, func() {
		// The page takes no second press either, and the update itself holds
		// a lock for a second window or a terminal; this is the third guard,
		// for this window's own binding.
		if !updating.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer updating.Store(false)
			if how.Refusal != "" {
				tell(w, refusalProgress(how.Refusal))
				return
			}
			runUpdate(w, *url, canonical, how.Source, kept)
		}()
	}); err != nil {
		log.Printf("fleetdeck-window: the update button will not work: %v", err)
	}
	// Looking for a newer version runs beside the window for as long as it is
	// open, silent unless what it knows changes. A window started by a
	// handover looks too: it stays open as long as the one it replaced would
	// have, and the answer kept for the version before it does not apply.
	if watch != nil {
		go watch.run(context.Background(), time.NewTicker(lookEvery).C)
	}

	// The window's own ground until the keeper's first word, which comes within
	// one look at the URL.
	w.SetHtml(blankPage)
	if *handover != "" {
		events := make(chan supervisor.Event, 16)
		takeoverEvents.Store(&events)
		tk := &supervisor.Takeover{
			URL:         *url,
			Handover:    supervisor.Handover{Path: *handover},
			Staged:      bundleOf(exe),
			Canonical:   *toldCanonical,
			Revision:    ownRevision(),
			Keeper:      keeper,
			StartKeeper: kept.start,
			Events:      events,
			// The bundle swapped out is forgotten by LaunchServices and then
			// removed, once the old window's update has let go of the lock
			// (docs/engineering/window-and-panel.md).
			Registry: supervisor.LaunchServices{Lsregister: supervisor.LsregisterPath},
			LockPath: updateLockPath(*toldCanonical),
			// The old window started this one, so it is this process's parent
			// until it exits, and then this process is handed to launchd: the
			// parent changing is the old window gone, with no PID to be reused.
			// Works with an old window of any version, which says nothing of
			// itself in the handover.
			OldWindowGone: func() bool { return os.Getppid() != oldWindow },
			Done: func() {
				w.Dispatch(func() {
					log.Printf("fleetdeck-window: the handover is done")
					if scr.handedOver() {
						show(true, "")
					}
				})
			},
			Logf: func(format string, args ...any) { log.Printf("fleetdeck-window: "+format, args...) },
		}
		go func() {
			err := tk.Run(context.Background())
			takeoverEvents.Store(nil)
			if err != nil {
				// The old window resumes the panel it had; this one goes.
				log.Printf("fleetdeck-window: taking the panel over failed: %v", err)
				kept.stop()
				w.Dispatch(w.Terminate)
			}
		}()
	} else {
		kept.start()
	}

	// Time going by for a page asked for and not loaded (screen.tick).
	ticking, stopTicking := context.WithCancel(context.Background())
	ticked := make(chan struct{})
	go func() {
		defer close(ticked)
		tick := time.NewTicker(pageLoadTick)
		defer tick.Stop()
		for {
			select {
			case <-ticking.Done():
				return
			case <-tick.C:
				w.Dispatch(func() { show(scr.tick()) })
			}
		}
	}()

	w.Run()
	stopTicking()
	<-ticked
	// The keeper stops here; the panel goes by itself once this process has
	// ended, having watched it. Waiting for the keeper keeps it from
	// dispatching onto a window already destroyed.
	kept.stop()
}

// updateLockPath is the update lock an update of canonical takes: beside the
// source tree for a build that has one, and otherwise beside the installed
// app, so that two windows of the same installed app cannot update it at once.
func updateLockPath(canonical string) string {
	root := treeDir
	if root == "" {
		root = canonical
	}
	path, err := supervisor.LockPath(root)
	if err != nil {
		return ""
	}
	return path
}

// tell hands one report to the page, from whatever goroutine is holding it.
func tell(w webview.WebView, r report) {
	w.Dispatch(func() { w.Eval(reportScript(r)) })
}

// runUpdate is one press of the update button: supervisor.Update over
// whichever source this build has, with each step handed to the page, and the
// window quitting once the new one has taken over.
func runUpdate(w webview.WebView, url, canonical string, source supervisor.Source, kept *keeperRun) {
	say := func(p supervisor.Progress) {
		w.Dispatch(func() { w.Eval(progressScript(p, "")) })
	}
	// The lock lives beside whatever this build updates from: a source tree
	// for a build that has one, and otherwise the installed app itself, so
	// that two windows of the same installed app cannot update it at once.
	lockRoot := treeDir
	if lockRoot == "" {
		lockRoot = canonical
	}
	lockPath, err := supervisor.LockPath(lockRoot)
	if err != nil {
		tell(w, resultProgress(err))
		return
	}
	var last string
	u := &supervisor.Update{
		Source:          source,
		Canonical:       canonical,
		LockPath:        lockPath,
		HandoverTimeout: handoverTimeout,
		Launch:          launchNewWindow(url),
		Pause:           kept.stop,
		Resume:          kept.start,
		Progress: func(p supervisor.Progress) {
			last = p.Step
			log.Printf("fleetdeck-window: update %s %s", p.Step, p.Detail)
			say(p)
		},
	}
	if err := u.Run(context.Background()); err != nil {
		log.Printf("fleetdeck-window: update: %v", err)
		tell(w, resultProgress(err))
		return
	}
	if last == "done" {
		// The new window has the panel and the canonical path; this one, and
		// the bundle it ran from, are the previous version.
		w.Dispatch(w.Terminate)
	}
}

// keeperRun starts and stops the keeper's Run: an update pauses it while the
// new window takes the panel over, and resumes it when that fails.
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

func describeEvent(e supervisor.Event) string {
	switch {
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.State, e.Err)
	case e.Detail != "":
		return fmt.Sprintf("%s: %s", e.State, e.Detail)
	case e.PID != 0:
		return fmt.Sprintf("%s (pid %d, started by this window)", e.State, e.PID)
	default:
		return fmt.Sprintf("%s (not started by this window)", e.State)
	}
}
