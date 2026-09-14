//go:build darwin

package main

import (
	"fmt"
	"html"
	"os/exec"
	"path/filepath"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// A window started from an update's staging directory, other than by that
// update, is not the installed app. On 2026-09-14 LaunchServices opened
// /Applications/.fleetdeck-update/fleetdeck.app -- the bundle an update had
// just swapped out -- as "fleetdeck". That window took its own bundle for the
// installed one, updated itself inside the staging directory, and left a
// .fleetdeck-update inside .fleetdeck-update, with the window running from
// there. Now such a window opens the installed app instead and goes, before
// it has a window or a panel. It touches nothing in the staging directory,
// where another update may be running: it only has LaunchServices forget its
// own bundle, so that the next "fleetdeck" opens the installed one.

// stagingDirName is what supervisor.StagingDir calls the staging directory.
var stagingDirName = filepath.Base(supervisor.StagingDir(supervisor.BundleName))

type runAction int

const (
	// runHere: this window is where it should run.
	runHere runAction = iota
	// runElsewhere: this window is in a staging directory, with the installed
	// app beside it; it opens that and goes.
	runElsewhere
	// runRefused: this window is in a staging directory with no installed app
	// beside it; it says so, and starts no panel.
	runRefused
)

type runWhere struct {
	action runAction
	// staged is this window's bundle, in a staging directory.
	staged string
	// installed is the app the staging directory is beside.
	installed string
	// err is why opening the installed app did not work, when it did not.
	err error
}

// whereToRun works out whether the window with binary exe runs here. A window
// started by an update's handover runs where it was started: that is how an
// update tries the new version before it puts it in place.
func whereToRun(exe, handover string, exists func(string) bool) runWhere {
	if handover != "" {
		return runWhere{action: runHere}
	}
	bundle := bundleOf(exe)
	installed, staged := installedBeside(bundle)
	if !staged {
		return runWhere{action: runHere}
	}
	w := runWhere{action: runElsewhere, staged: bundle, installed: installed}
	if !exists(installed) {
		w.action = runRefused
	}
	return w
}

// installedBeside is the installed app for a bundle in a staging directory,
// and whether bundle is in one. The installed app is above every staging
// directory the bundle is in, so it is never itself in one: a window started
// from it runs there, and nothing opens the same window again.
func installedBeside(bundle string) (string, bool) {
	if bundle == "" {
		return "", false
	}
	dir := filepath.Dir(bundle)
	if filepath.Base(dir) != stagingDirName {
		return "", false
	}
	for filepath.Base(dir) == stagingDirName {
		dir = filepath.Dir(dir)
	}
	return filepath.Join(dir, supervisor.BundleName), true
}

// goToInstalled has LaunchServices forget this window's bundle and opens the
// installed app, by its path: measured on 2026-09-14, `open <path>` opens that
// bundle whatever else is registered under the identifier.
func goToInstalled(w runWhere) error {
	ls := supervisor.LaunchServices{Lsregister: supervisor.LsregisterPath}
	if err := ls.Forget(w.staged); err != nil {
		// Said, not fatal: the installed app still opens by its path.
		fmt.Printf("fleetdeck-window: LaunchServices still knows %s: %v\n", w.staged, err)
	}
	if out, err := exec.Command("/usr/bin/open", w.installed).CombinedOutput(); err != nil {
		return fmt.Errorf("open %s: %w: %s", w.installed, err, out)
	}
	return nil
}

// stagedPage is what a window in a staging directory shows when it cannot
// open the installed app.
func stagedPage(w runWhere) string {
	why := "Установленного приложения рядом нет: <code>" + html.EscapeString(w.installed) + "</code> не найдено."
	if w.err != nil {
		why = "Открыть установленное приложение <code>" + html.EscapeString(w.installed) + "</code> не удалось: " + html.EscapeString(w.err.Error())
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>fleetdeck</title>
%s
</head>
<body>
<main>
  <h1>Это не установленное приложение</h1>
  <p>Окно запущено из <code>%s</code> — каталога, куда обновление кладёт новую версию и где после обмена остаётся прежняя.</p>
  <p>%s</p>
  <p>Поэтому окно не запускает панель и не обновляется. Откройте установленный fleetdeck или установите его заново.</p>
</main>
</body>
</html>`, pageStyle, html.EscapeString(w.staged), why)
}
