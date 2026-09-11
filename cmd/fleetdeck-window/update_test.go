//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// The two names are a contract across two languages: the window binds one and
// calls the other, the page looks for the first and defines the second.
func TestThePageUsesTheUpdateNamesTheWindowGivesIt(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "js", "update.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`UPDATE_BINDING = "` + updateBindingName + `"`,
		`PROGRESS_FUNCTION = "` + progressFunction + `"`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("web/js/update.js does not say %s", want)
		}
	}
}

func TestTheBundleIsFoundFromTheWindowInsideIt(t *testing.T) {
	for exe, want := range map[string]string{
		"/Users/op/claude/fleetdeck/bin/fleetdeck.app/Contents/MacOS/fleetdeck-window": "/Users/op/claude/fleetdeck/bin/fleetdeck.app",
		// Not in a bundle: a plain `make build`.
		"/Users/op/claude/fleetdeck/bin/fleetdeck-window": "",
		// Shaped almost like one, but not.
		"/Users/op/fleetdeck/Contents/MacOS/fleetdeck-window": "",
	} {
		if got := bundleOf(exe); got != want {
			t.Errorf("bundleOf(%q) = %q, want %q", exe, got, want)
		}
	}
}

// After a handover the new window's own path is where the bundle was built,
// and that is where the old bundle now lies. Its next update must replace the
// canonical bundle, which it was told, not the one its path points at.
func TestTheCanonicalBundleIsTheOneTheWindowWasToldAfterAHandover(t *testing.T) {
	exe := "/Users/op/claude/fleetdeck/bin/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	if got := canonicalBundle(exe, "/Users/op/claude/fleetdeck/bin/fleetdeck.app"); got != "/Users/op/claude/fleetdeck/bin/fleetdeck.app" {
		t.Fatalf("canonicalBundle = %q, want the one it was told", got)
	}
	if got := canonicalBundle("/a/fleetdeck.app/Contents/MacOS/fleetdeck-window", ""); got != "/a/fleetdeck.app" {
		t.Fatalf("canonicalBundle without a handover = %q, want its own bundle", got)
	}
}

func TestAWindowThatCannotUpdateItselfSaysWhy(t *testing.T) {
	inBundle := "/a/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	if why := updateUnavailable("", inBundle); !strings.Contains(why, "source tree") {
		t.Fatalf("no tree written in: %q, want the reason", why)
	}
	if why := updateUnavailable("/src/fleetdeck", "/a/bin/fleetdeck-window"); !strings.Contains(why, "app bundle") {
		t.Fatalf("not in a bundle: %q, want the reason", why)
	}
	if why := updateUnavailable("/src/fleetdeck", inBundle); why != "" {
		t.Fatalf("a bundled window with its tree cannot update: %q", why)
	}
}

func TestProgressReachesThePageAsOneCallWithItsTextQuoted(t *testing.T) {
	script := progressScript(supervisor.Progress{Step: "failed", Detail: `a "quoted" </script> detail`})
	prefix := "window." + progressFunction + " && window." + progressFunction + "("
	if !strings.HasPrefix(script, prefix) || !strings.HasSuffix(script, ")") {
		t.Fatalf("script %q is not one guarded call", script)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(script, prefix), ")")), &got); err != nil {
		t.Fatalf("the argument is not JSON: %v (%s)", err, script)
	}
	if got["step"] != "failed" || got["detail"] != `a "quoted" </script> detail` {
		t.Fatalf("the page would get %v", got)
	}
}

func TestAnUpdatesEndIsToldToThePageInItsOwnWords(t *testing.T) {
	if p := resultProgress(supervisor.ErrBusy); p.Step != "busy" {
		t.Fatalf("ErrBusy -> %+v, want busy", p)
	}
	if p := resultProgress(errors.New("the source tree has uncommitted edits (a.go)")); p.Step != "failed" || !strings.Contains(p.Detail, "a.go") {
		t.Fatalf("an error -> %+v, want it failed with the error's words", p)
	}
}

// The new window, taking over, is not starting a panel because none was
// there: it says what it is doing.
func TestTheNewWindowSaysItIsTakingOverNotStartingFromNothing(t *testing.T) {
	s := &screen{url: testURL, logPath: "/log", takingOver: true}
	_, html := s.on(supervisor.Event{State: supervisor.Starting, PID: 9})
	if !strings.Contains(html, "Принимаю пульт") || strings.Contains(html, "никто не отвечал") {
		t.Fatalf("taking over, the starting page reads: %q", html)
	}
}

// A stub has a test that fails while it is one: the handover timeout is a
// placeholder until ten real handovers are measured.
func TestTheHandoverTimeoutIsTheMeasuredWorstTimesThree(t *testing.T) {
	if measuredWorstHandover*handoverMargin != handoverTimeout || handoverMargin != 3 {
		t.Fatalf("handoverTimeout = %s, want %s x 3", handoverTimeout, measuredWorstHandover)
	}
}
