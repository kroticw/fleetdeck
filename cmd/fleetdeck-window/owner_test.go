//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

const testURL = "http://127.0.0.1:7777/"

func TestThePanelIsLookedForBesideTheWindow(t *testing.T) {
	for exe, want := range map[string]string{
		// Inside the app bundle.
		"/Users/op/claude/fleetdeck/bin/fleetdeck.app/Contents/MacOS/fleetdeck-window": "/Users/op/claude/fleetdeck/bin/fleetdeck.app/Contents/MacOS/fleetdeck",
		// A plain `make build`, where both land in bin/.
		"/Users/op/claude/fleetdeck/bin/fleetdeck-window": "/Users/op/claude/fleetdeck/bin/fleetdeck",
	} {
		if got := panelBinary(exe); got != want {
			t.Errorf("panelBinary(%q) = %q, want %q", exe, got, want)
		}
	}
}

// Where the Makefile puts the panel and where the window looks for it are two
// halves of one agreement, in two languages. The bundle is built the way the
// operator builds it, and the panel must be exactly where panelBinary says.
func TestTheAppBundleCarriesThePanelWhereTheWindowLooksForIt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the whole app bundle")
	}
	const probe = "bundle-probe"
	bindir := t.TempDir()
	cmd := exec.Command("make", "window-app", "BINDIR="+bindir, "VERSION="+probe)
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make window-app: %v\n%s", err, out)
	}
	window := filepath.Join(bindir, "fleetdeck.app", "Contents", "MacOS", "fleetdeck-window")
	for _, bin := range []string{window, panelBinary(window)} {
		info, err := os.Stat(bin)
		if err != nil {
			t.Fatalf("the bundle lacks %s: %v", bin, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("%s is not executable", bin)
		}
	}
	// The panel in the bundle is the panel, not a second copy of the window.
	// Checked by its links before it is run: a window started here would open
	// on whatever screen runs the tests.
	if webkit, err := hasWebKitDylib(panelBinary(window)); err != nil || webkit {
		t.Fatalf("%s links WebKit (%v, %v): it is a window, not the panel", panelBinary(window), webkit, err)
	}
	out, err := exec.Command(panelBinary(window), "version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != probe {
		t.Fatalf("%s version = %q (%v), want %q from this build", panelBinary(window), out, err, probe)
	}
}

func TestTheScreenOpensThePanelOnceItAnswers(t *testing.T) {
	s := &screen{url: testURL, logPath: "/log"}
	navigate, html := s.on(supervisor.Event{State: supervisor.Answering})
	if !navigate || html != "" {
		t.Fatalf("on(Answering) = %v, %q; want to navigate to the panel", navigate, html)
	}
	// Once it shows, a second Answering -- the keeper taking a panel after a
	// restart -- does not reload the page under the person.
	if navigate, html := s.on(supervisor.Event{State: supervisor.Answering, Ours: true}); navigate || html != "" {
		t.Fatalf("second on(Answering) = %v, %q; want nothing", navigate, html)
	}
}

func TestTheScreenSaysThePanelIsStartingWhenNothingShowsYet(t *testing.T) {
	s := &screen{url: testURL, logPath: "/log"}
	navigate, html := s.on(supervisor.Event{State: supervisor.Starting, PID: 42})
	if navigate || !strings.Contains(html, "Запускаю панель") {
		t.Fatalf("on(Starting) = %v, %q; want the starting page", navigate, html)
	}
}

// A panel that died and is being started again is not covered over: its own
// page already says it is offline and reconnects by itself, keeping whatever
// the person had open.
func TestTheScreenLeavesThePanelsPageAloneDuringARestart(t *testing.T) {
	s := &screen{url: testURL, logPath: "/log"}
	s.on(supervisor.Event{State: supervisor.Answering})
	if navigate, html := s.on(supervisor.Event{State: supervisor.Starting, PID: 43}); navigate || html != "" {
		t.Fatalf("on(Starting) over the panel = %v, %q; want nothing", navigate, html)
	}
}

func TestTheScreenShowsAFailureEvenOverThePanelAndOpensThePanelAgainAfter(t *testing.T) {
	s := &screen{url: testURL, logPath: "/log"}
	s.on(supervisor.Event{State: supervisor.Answering})

	_, html := s.on(supervisor.Event{State: supervisor.Failed, Err: errors.New("the panel ended: signal: killed")})
	if !strings.Contains(html, "Панель не запустилась") {
		t.Fatalf("on(Failed) html = %q; want the failure page", html)
	}
	s.on(supervisor.Event{State: supervisor.Starting, PID: 44})
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true}); !navigate {
		t.Fatal("after a failure, a panel that answers again is not opened")
	}
}

func TestTheStartingPageShowsAWaitLongerThanTwoSeconds(t *testing.T) {
	page := startingPage(testURL)
	if !strings.Contains(page, testURL) {
		t.Fatal("the starting page does not name the address")
	}
	if waitShownAfter.Milliseconds() != 2000 {
		t.Fatalf("waitShownAfter = %s, the agreed threshold is 2 s", waitShownAfter)
	}
	if !strings.Contains(page, fmt.Sprintf(">= %d", waitShownAfter.Milliseconds())) {
		t.Fatal("the starting page does not show the time once the wait passes the threshold")
	}
}

func TestTheFailurePageSaysWhyShowsTheLogAndOffersToStartAgain(t *testing.T) {
	page := failedPage(testURL, supervisor.Event{
		State:   supervisor.Failed,
		Err:     errors.New("the panel (pid 7) ended: exit status 1"),
		LogTail: "fleetdeck: load config: yaml: line 3: bad indentation",
	}, "/Users/op/Library/Logs/fleetdeck.log")
	for _, want := range []string{
		"the panel (pid 7) ended: exit status 1",
		"load config: yaml: line 3: bad indentation",
		"/Users/op/Library/Logs/fleetdeck.log",
		"Запустить снова",
		"window." + startBindingName + "()",
		// The button changes on the first press, so a second press does not
		// look like the first one did nothing.
		"disabled = true",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the failure page lacks %q", want)
		}
	}
}

func TestPagesEscapeEverythingTheyInterpolate(t *testing.T) {
	const evil = `"><script>alert(1)</script>`
	for name, page := range map[string]string{
		"starting":  startingPage("http://x/" + evil),
		"replacing": replacingPage(evil),
		"failed": failedPage("http://x/"+evil, supervisor.Event{
			State: supervisor.Failed, Err: errors.New(evil), LogTail: evil,
		}, "/log/"+evil),
	} {
		if strings.Contains(page, "<script>alert(1)</script>") {
			t.Errorf("the %s page carries the interpolated markup unescaped", name)
		}
	}
}

// The window names itself as the panel's owner in the panel's own flag: two
// halves of one agreement, in two programs. Checked against the real panel:
// told that PID 1 started it -- which never does -- the panel must refuse,
// naming the flag. A panel that did not know the flag would fail differently;
// one that ignored it would start.
func TestThePanelUnderstandsHowTheWindowNamesItsOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the panel")
	}
	panel := filepath.Join(t.TempDir(), "fleetdeck")
	build := exec.Command("go", "build", "-o", panel, "./cmd/fleetdeck")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/fleetdeck: %v\n%s", err, out)
	}
	// Refused before anything is read, but kept off the machine's config,
	// board and fleet daemon anyway: HOME of its own, and a -stand-socket
	// nothing listens on (the daemon is found by uid, not by HOME).
	dir := t.TempDir()
	cmd := exec.Command(panel, append([]string{"--stand-socket", filepath.Join(dir, "no-daemon.sock")}, panelArgs(1)...)...)
	cmd.Env = append(os.Environ(), "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the panel started though PID 1 is not its parent:\n%s", out)
	}
	if !strings.Contains(string(out), "started with --owner-pid 1, but this panel's parent is") {
		t.Fatalf("the panel failed, but not by refusing the owner: %v\n%s", err, out)
	}
}

func TestThePanelIsToldWhichWindowItBelongsTo(t *testing.T) {
	got := panelArgs(4242)
	if want := []string{"--owner-pid", "4242"}; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("panelArgs(4242) = %q, want %q", got, want)
	}
}

// A panel an earlier window left behind is being stopped: the window says so,
// over the panel's page too -- the page belongs to the panel being stopped --
// and opens the new panel once it answers.
func TestTheScreenSaysALeftBehindPanelIsBeingReplacedAndOpensTheNewOne(t *testing.T) {
	s := &screen{url: testURL, logPath: "/log"}
	s.on(supervisor.Event{State: supervisor.Answering})
	const whose = "its window (pid 777) is gone"
	navigate, html := s.on(supervisor.Event{State: supervisor.Replacing, Detail: whose})
	if navigate || !strings.Contains(html, "Заменяю панель") || !strings.Contains(html, whose) {
		t.Fatalf("on(Replacing) = %v, %q; want the replacing page naming whose panel it was", navigate, html)
	}
	s.on(supervisor.Event{State: supervisor.Starting, PID: 45})
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true}); !navigate {
		t.Fatal("after a replacement, the new panel is not opened")
	}
}

func TestTheLogSaysWhosePanelIsBeingReplaced(t *testing.T) {
	const whose = "its window (pid 777) is gone"
	if got := describeEvent(supervisor.Event{State: supervisor.Replacing, Detail: whose}); !strings.Contains(got, whose) {
		t.Fatalf("describeEvent(Replacing) = %q, want it to say %q", got, whose)
	}
}

func TestTheStartTimeoutIsTheMeasuredWorstTimesThree(t *testing.T) {
	if measuredWorstStart*startMargin != panelStartTimeout {
		t.Fatalf("panelStartTimeout = %s, want %s x %d", panelStartTimeout, measuredWorstStart, startMargin)
	}
}
