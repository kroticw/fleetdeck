//go:build darwin

package main

import (
	"strings"
	"testing"
)

// On 2026-09-14 a window started from /Applications/.fleetdeck-update -- the
// bundle an update had just swapped out, opened by LaunchServices -- took its
// own bundle for the installed app and updated itself inside the staging
// directory, into .fleetdeck-update/.fleetdeck-update. A window that finds
// itself in a staging directory, and was not started there by an update, is
// not the installed app: it goes to the installed one.

func exists(paths ...string) func(string) bool {
	return func(p string) bool {
		for _, q := range paths {
			if p == q {
				return true
			}
		}
		return false
	}
}

func TestAWindowStartedFromTheStagingDirectoryOpensTheInstalledApp(t *testing.T) {
	exe := "/Applications/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	got := whereToRun(exe, "", exists("/Applications/fleetdeck.app"))
	if got.action != runElsewhere || got.installed != "/Applications/fleetdeck.app" || got.staged != "/Applications/.fleetdeck-update/fleetdeck.app" {
		t.Fatalf("whereToRun(%q) = %+v; want to open /Applications/fleetdeck.app", exe, got)
	}
}

// The nested staging directory the incident left: the installed app is the
// one above every staging directory, never one inside another.
func TestTheInstalledAppIsNeverInsideAStagingDirectory(t *testing.T) {
	for _, exe := range []string{
		"/Applications/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window",
		"/Applications/.fleetdeck-update/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window",
		"/Users/op/Applications/.fleetdeck-update/.fleetdeck-update/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window",
	} {
		got := whereToRun(exe, "", func(string) bool { return true })
		if got.action != runElsewhere {
			t.Fatalf("whereToRun(%q) = %+v; want to open the installed app", exe, got)
		}
		if strings.Contains(got.installed, ".fleetdeck-update") {
			t.Fatalf("whereToRun(%q) names %s as the installed app: a staging directory, which would start the same window again", exe, got.installed)
		}
		if again := whereToRun(got.installed+"/Contents/MacOS/fleetdeck-window", "", func(string) bool { return true }); again.action != runHere {
			t.Fatalf("the installed app %s, started, would go elsewhere again: %+v", got.installed, again)
		}
	}
}

// With nothing installed beside it, the window says so rather than quitting
// in silence, and names where it looked.
func TestAWindowInAStagingDirectoryWithNothingInstalledBesideItSaysSo(t *testing.T) {
	exe := "/Applications/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	got := whereToRun(exe, "", exists())
	if got.action != runRefused || got.installed != "/Applications/fleetdeck.app" {
		t.Fatalf("whereToRun(%q) with nothing installed = %+v; want a refusal naming /Applications/fleetdeck.app", exe, got)
	}
	page := stagedPage(got)
	for _, want := range []string{"/Applications/.fleetdeck-update/fleetdeck.app", "/Applications/fleetdeck.app"} {
		if !strings.Contains(page, want) {
			t.Errorf("the refusal page does not name %s:\n%s", want, page)
		}
	}
}

// The new window of an update runs from the staging directory on purpose.
func TestTheNewWindowOfAnUpdateRunsWhereItWasStarted(t *testing.T) {
	exe := "/Applications/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	if got := whereToRun(exe, "/Applications/.fleetdeck-update/handover", exists("/Applications/fleetdeck.app")); got.action != runHere {
		t.Fatalf("whereToRun with a handover = %+v; want to run here", got)
	}
}

// Everywhere else the window runs where it is: an installed app, a stand in a
// temporary directory, a bare binary.
func TestAWindowAnywhereElseRunsWhereItIs(t *testing.T) {
	for _, exe := range []string{
		"/Applications/fleetdeck.app/Contents/MacOS/fleetdeck-window",
		"/private/var/folders/x/T/stand/fleetdeck.app/Contents/MacOS/fleetdeck-window",
		"/Users/op/claude/fleetdeck/bin/fleetdeck-window",
	} {
		if got := whereToRun(exe, "", func(string) bool { return true }); got.action != runHere {
			t.Errorf("whereToRun(%q) = %+v; want to run here", exe, got)
		}
	}
}

// A window in a staging directory cannot update itself either, whatever else
// happens: its own bundle is not the installed app.
func TestAWindowInAStagingDirectoryDoesNotTakeItsOwnBundleForTheInstalledOne(t *testing.T) {
	exe := "/Applications/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	if got := canonicalBundle(exe, ""); got != "" {
		t.Fatalf("canonicalBundle(%q, \"\") = %q; want none", exe, got)
	}
	if how := updateWay(config{exe: exe, version: "v0.9.1", teamID: "TEAM"}); how.Refusal != refusalStaged || how.Source != nil {
		t.Fatalf("updateWay from a staging directory = %+v; want the %q refusal", how, refusalStaged)
	}
	// Told by an update, it is the canonical path it was told.
	if got := canonicalBundle(exe, "/Applications/fleetdeck.app"); got != "/Applications/fleetdeck.app" {
		t.Fatalf("canonicalBundle with --canonical = %q", got)
	}
}

// The new window of an update runs from the staging directory and is told the
// installed bundle: it updates that, as the window it replaced did. Refusing
// it -- as the first version of the staged refusal did, seen on the T-057
// stand -- would take the update button away after every update until the
// app was next started.
func TestTheNewWindowOfAnUpdateCanUpdateTheBundleItWasToldOf(t *testing.T) {
	exe := "/Applications/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	how := updateWay(config{exe: exe, version: "v0.9.2", teamID: "TEAM", canonical: "/Applications/fleetdeck.app"})
	if how.Refusal != "" || how.Source == nil {
		t.Fatalf("updateWay for the new window of an update = %+v; want a release source", how)
	}
}
