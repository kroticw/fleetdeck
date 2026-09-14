//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// Written in by `make window-app` (-ldflags -X): the source tree this app was
// built from, and the tools that built it. The update button brings that tree
// forward and builds it again. An app built any other way has no tree here,
// and updates by downloading a release instead (way.go) -- it does not, as it
// once did, go without a button.
var (
	treeDir  string
	gitPath  string
	goPath   string
	makePath string
)

// The branch of its remote the tree follows.
const (
	updateRemote = "origin"
	updateBranch = "master"
)

const (
	updateBindingName = "fleetdeckUpdate"
	progressFunction  = "fleetdeckUpdateProgress"
	// knownBindingName is what the page asks, as it loads, about a newer
	// version the window already knows of. The name is a contract across two
	// languages, and update_test.go holds both sides to the same spelling.
	knownBindingName = "fleetdeckUpdateKnown"
)

// How long the new window has, from its start to its panel answering from the
// canonical path: the worst of ten measured handovers, three times over.
//
// Measured on 2026-09-11 on the operator's machine, on his screen at a moment
// he gave for it, with the fleet running: ten handovers, each a real one -- a
// new window from a freshly built bundle of its own (ten different binaries,
// each run for the first time, as after every update), started with
// --handover and --canonical against a live panel from the canonical bundle,
// on a stand with a HOME and a port of its own. Timed from the new window's
// start to "done" in the handover file. In ms: 812, 762, 542, 495, 490, 489,
// 490, 485, 484, 483 -- the first two slower, the rest steady. Not measured: a
// handover straight after login, with nothing of the binaries in the disk
// cache.
const (
	measuredWorstHandover = 812 * time.Millisecond
	handoverMargin        = 3
	handoverTimeout       = measuredWorstHandover * handoverMargin
)

// handoverTimeoutFlag is how a window tells the new window it starts how long
// it gives the handover (newWindowArgs).
const handoverTimeoutFlag = "handover-timeout"

// oldWindowHandoverTimeoutV0100 is how long a window of v0.10.0 gives the
// whole handover: its handoverTimeout, 812 ms three times over, as measured
// above, written into it. A window of v0.10.0 or before starts the new window
// without --handover-timeout and cannot be asked. It is not handoverTimeout:
// that is this build's own, which a later build may change while windows of
// v0.10.0 are still installed.
const oldWindowHandoverTimeoutV0100 = 2436 * time.Millisecond

// oldWindowHandoverTimeout is how long the window that started this one gives
// the handover: what it said, or v0.10.0's when it said nothing.
func oldWindowHandoverTimeout(told time.Duration) time.Duration {
	if told > 0 {
		return told
	}
	return oldWindowHandoverTimeoutV0100
}

// handoverDeadline is when the window that started this one gives up on the
// handover. Its clock starts once it has started this process, so counting
// from the kernel's start time for this process ends no later than it does;
// counting from when main runs would end later, by however long this process
// took to get there.
func handoverDeadline(told time.Duration) time.Time {
	started, err := processStart()
	if err != nil {
		log.Printf("fleetdeck-window: this process's start time is unknown (%v); the handover deadline counts from now", err)
		started = time.Now()
	}
	return started.Add(oldWindowHandoverTimeout(told))
}

// processStart is when the kernel started this process.
func processStart() (time.Time, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", os.Getpid())
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(kp.Proc.P_starttime.Unix()), nil
}

// newWindowArgs is what the new window is started with: where the panel
// answers, the handover file, the installed bundle, and how long this window
// gives the handover.
func newWindowArgs(url, canonical, handover string) []string {
	return []string{
		"--url", url,
		"--handover", handover,
		"--canonical", canonical,
		"--" + handoverTimeoutFlag, handoverTimeout.String(),
	}
}

// bundleOf is the .app bundle exe sits in, or "" when it is not in one.
func bundleOf(exe string) string {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || filepath.Ext(app) != ".app" {
		return ""
	}
	return app
}

// canonicalBundle is the installed bundle this window updates: the one it was
// told when it was started by a handover -- its own path is then where the
// old bundle lies -- and otherwise its own, unless its own is in a staging
// directory, which is never the installed app (staged.go). Taking it for one
// is how an update came to run inside .fleetdeck-update on 2026-09-14.
func canonicalBundle(exe, told string) string {
	if told != "" {
		return told
	}
	bundle := bundleOf(exe)
	if _, staged := installedBeside(bundle); staged {
		return ""
	}
	return bundle
}

// ownRevision is the commit this window was built from.
func ownRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

// progressScript hands one step to the page: one guarded call, its argument
// JSON -- which, being a JS literal too, needs nothing escaped for the eval.
// The reason travels beside the step so the page can say why in the reader's
// own language; the detail is this refusal's particulars, whatever language
// they are in.
func progressScript(p supervisor.Progress, reason string) string {
	arg, _ := json.Marshal(report{Step: p.Step, Reason: reason, Detail: p.Detail})
	return fmt.Sprintf("window.%s && window.%s(%s)", progressFunction, progressFunction, arg)
}

// reportScript is progressScript for a report the window made itself.
func reportScript(r report) string {
	arg, _ := json.Marshal(r)
	return fmt.Sprintf("window.%s && window.%s(%s)", progressFunction, progressFunction, arg)
}

// launchNewWindow starts the window in the staged bundle as the one to take
// the panel over. Its output goes beside the handover file, where the old
// window's failure report can point.
func launchNewWindow(url string) func(staged, canonical, handover string) (func(), error) {
	return func(staged, canonical, handover string) (func(), error) {
		logPath := filepath.Join(filepath.Dir(handover), supervisor.NewWindowLog)
		log, err := os.Create(logPath)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(filepath.Join(staged, "Contents", "MacOS", "fleetdeck-window"),
			newWindowArgs(url, canonical, handover)...)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			_ = log.Close()
			return nil, err
		}
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			_ = log.Close()
			close(done)
		}()
		return func() {
			_ = cmd.Process.Kill()
			<-done
		}, nil
	}
}
