//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The acceptance for the notice about a panel of another build, on a built
// app and a real panel of another build: a window started as a separate
// process, a page loaded in it, and the window's log as the record of what
// the page painted -- a test cannot look at the screen.
//
// It needs two builds made for it, and it is skipped without them: an app
// built the way a release is, and the panel of some other commit.
//
//	make dist-app DISTDIR=/tmp/t055 VERSION=v0.9.1-stand DIST_ARCHES=arm64
//	ditto -x -k /tmp/t055/fleetdeck-v0.9.1-stand-macos.zip /tmp/t055/app
//	git clone --no-checkout . /tmp/t055/src && git -C /tmp/t055/src checkout de3aa16
//	go -C /tmp/t055/src build -o /tmp/t055/other/fleetdeck ./cmd/fleetdeck
//	FLEETDECK_STAND_APP=/tmp/t055/app/fleetdeck.app FLEETDECK_STAND_OTHER_PANEL=/tmp/t055/other/fleetdeck \
//	    go test ./cmd/fleetdeck-window/ -run TestAStandWindow -v -timeout 10m
//
// Each case opens one window on the screen and closes it before the next.
// Every process has a HOME and a port of its own; the panels this test starts
// are given a -stand-socket nothing listens on. The panel the window starts
// is started by the window with its own arguments, so it can be given none,
// and it finds the fleet daemon by uid: nothing here opens a session in it.
// The launch agent is a file in the test's HOME, and is never loaded.
func TestAStandWindowNamesAPanelOfAnotherBuildAndIsQuietOverItsOwn(t *testing.T) {
	app := os.Getenv("FLEETDECK_STAND_APP")
	other := os.Getenv("FLEETDECK_STAND_OTHER_PANEL")
	if app == "" || other == "" {
		t.Skip("set FLEETDECK_STAND_APP and FLEETDECK_STAND_OTHER_PANEL to run the acceptance")
	}
	window := filepath.Join(app, "Contents", "MacOS", "fleetdeck-window")

	t.Run("a panel of another build that the launch agent keeps", func(t *testing.T) {
		home, url := standHome(t)
		agent := writeAgent(t, home, launchAgentLabel, other)
		panel := startStandPanel(t, other, home, url)
		w := startStandWindow(t, window, url)

		w.waitFor(t, "is not this app's build")
		w.waitFor(t, "the page shows the notice about the panel")
		log := w.text()
		for _, want := range []string{other, launchAgentLabel + ` ("` + agent + `") keeps it running`, "launchctl bootout gui/$(id -u)/" + launchAgentLabel} {
			if !strings.Contains(log, want) {
				t.Errorf("the window's log lacks %q", want)
			}
		}
		if strings.Contains(log, "Заменить панелью приложения") {
			t.Error("the page offers to replace a panel the launch agent keeps")
		}

		// The agent removed, the panel goes: the window starts its own, of its
		// own build, and takes the notice down.
		_ = panel.Process.Kill()
		w.waitFor(t, "the notice about the panel at "+url+" is taken down")
		w.waitFor(t, "the page took the notice about the panel down")
		w.waitFor(t, "answering (pid")
	})

	t.Run("a panel of another build started from a terminal", func(t *testing.T) {
		home, url := standHome(t)
		startStandPanel(t, other, home, url)
		w := startStandWindow(t, window, url)

		w.waitFor(t, "is not this app's build")
		w.waitFor(t, "the page shows the notice about the panel")
		log := w.text()
		if !strings.Contains(log, "Заменить панелью приложения") {
			t.Error("the page does not offer to replace a panel started from a terminal")
		}
		if strings.Contains(log, launchAgentLabel) {
			t.Error("the notice names a launch agent where there is none")
		}
	})

	t.Run("a panel of the app's own build started from a terminal", func(t *testing.T) {
		home, url := standHome(t)
		// Out of the bundle: a panel run from inside one reporting no window is
		// replaced as one an old window left behind (Keeper.replaceable).
		own := filepath.Join(home, "bin", "fleetdeck")
		copyExecutable(t, filepath.Join(app, "Contents", "MacOS", "fleetdeck"), own)
		startStandPanel(t, own, home, url)
		w := startStandWindow(t, window, url)

		w.waitFor(t, "panel answering (not started by this window)")
		// Time for the page to load and ask the window for a notice.
		time.Sleep(5 * time.Second)
		log := w.text()
		if strings.Contains(log, "is not this app's build") || strings.Contains(log, "shows the notice") {
			t.Errorf("the window put up a notice over a panel of its own build:\n%s", log)
		}
	})
}

// standWait bounds every wait for a line in a stand window's log: a window
// starting, a page loading, the keeper noticing a panel gone.
const standWait = 30 * time.Second

// standHome is a HOME of the case's own, with a configuration naming a free
// port, and the URL of that port.
func standHome(t *testing.T) (home, url string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	url = freeURL(t)
	writeStandConfig(t, home, portOf(t, url))
	return home, url
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

// startStandPanel starts bin the way a terminal would, and stops it when the
// case ends.
func startStandPanel(t *testing.T, bin, home, url string) *exec.Cmd {
	t.Helper()
	out, err := os.Create(filepath.Join(home, "terminal-panel.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--stand-socket", filepath.Join(home, "no-daemon.sock"))
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); _ = out.Close(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	if !answers(url, 20*time.Second) {
		data, _ := os.ReadFile(out.Name())
		t.Fatalf("the panel %s never answered at %s:\n%s", bin, url, data)
	}
	return cmd
}

type standWindow struct {
	log string
}

// startStandWindow starts the stand's window on url, its log in a file, and
// ends it when the case ends; its panel goes with it by itself.
func startStandWindow(t *testing.T, bin, url string) *standWindow {
	t.Helper()
	if bundle := bundleOf(bin); bundle != "" {
		forgetBundle(t, bundle)
	}
	w := &standWindow{log: filepath.Join(t.TempDir(), "window.log")}
	out, err := os.Create(w.log)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--url", url)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); _ = out.Close(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
		t.Logf("the window's log:\n%s", w.text())
	})
	return w
}

func (w *standWindow) text() string {
	data, _ := os.ReadFile(w.log)
	return string(data)
}

func (w *standWindow) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(standWait)
	for time.Now().Before(deadline) {
		if strings.Contains(w.text(), want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the window's log did not say %q within %s", want, standWait)
}
