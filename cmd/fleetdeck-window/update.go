//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// Written in by `make window-app` (-ldflags -X): the source tree this app was
// built from, and the tools that built it. The update button brings that
// tree forward and builds it again; an app built any other way has no tree to
// update from, and shows no button.
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
// old bundle lies -- and otherwise its own.
func canonicalBundle(exe, told string) string {
	if told != "" {
		return told
	}
	return bundleOf(exe)
}

// updateUnavailable says why this window cannot update itself, or "" when it
// can.
func updateUnavailable(tree, exe string) string {
	if tree == "" {
		return "this app was built without its source tree written in (make window-app writes it)"
	}
	if bundleOf(exe) == "" {
		return "this window does not run from an app bundle"
	}
	return ""
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

// progressScript hands p to the page: one guarded call, its argument JSON --
// which, being a JS literal too, needs nothing escaped for the eval.
func progressScript(p supervisor.Progress) string {
	arg, _ := json.Marshal(map[string]string{"step": p.Step, "detail": p.Detail})
	return fmt.Sprintf("window.%s && window.%s(%s)", progressFunction, progressFunction, arg)
}

// resultProgress is how an update's error reaches the page.
func resultProgress(err error) supervisor.Progress {
	if errors.Is(err, supervisor.ErrBusy) {
		return supervisor.Progress{Step: "busy"}
	}
	return supervisor.Progress{Step: "failed", Detail: err.Error()}
}

// launchNewWindow starts the window in the staged bundle as the one to take
// the panel over. Its output goes beside the handover file, where the old
// window's failure report can point.
func launchNewWindow(url string) func(staged, canonical, handover string) (func(), error) {
	return func(staged, canonical, handover string) (func(), error) {
		logPath := filepath.Join(filepath.Dir(handover), "new-window.log")
		log, err := os.Create(logPath)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(filepath.Join(staged, "Contents", "MacOS", "fleetdeck-window"),
			"--url", url, "--handover", handover, "--canonical", canonical)
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
