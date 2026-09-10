package main

// No "import \"C\"" here on purpose -- Go test files cannot use cgo
// directly (golang/go#4030). Every cgo call these tests need lives in
// testsupport_darwin.go as plain Go functions; this file only calls those.
//
// AppKit's own rule forces a second thing: "go test" runs every Test
// function on its own freshly spawned goroutine, none of which is the
// process's real OS main thread -- and AppKit refuses menu/window mutation
// from anywhere else ("API misuse: setting the main menu on a non-main
// thread", hit and confirmed live, not assumed). TestMain below is the one
// place guaranteed to still run on the original goroutine before any test
// spawns; it makes every native call once there and stores the results,
// and the Test functions that follow only ever read those results.
//
// What this proves and what it does not: that the menu is built, that the
// operator's own five broken commands (Paste, Copy, Cut, Select All, and
// the Undo they found after) each have a menu item with the right selector
// and key equivalent, and that the close-to-hide delegate actually refuses
// the close and hides the window rather than merely being attached. None of
// this proves Cmd+V inserts text for a human pressing it -- that gap is
// exactly why an element existing and a human being able to use it are
// different claims (see the paste-vs-select-all screenshot lesson from
// earlier today). Acceptance of the actual keystrokes is the operator's,
// by hand, not a test's.

import (
	"os"
	"runtime"
	"testing"
	"unsafe"
)

var (
	mainMenuTopLevelCount int
	hasEditMenu           bool
	hasAppMenu            bool
	editMenuActionKeys    map[string]string
	quitKey               string
	quitKeyOK             bool

	closeHideWindow             unsafe.Pointer
	closeHideShouldCloseResult  int
	closeHideVisibleAfterClose  bool
	closeHideAppDelegateSet     bool
	closeHideReopenResult       bool
	closeHideVisibleAfterReopen bool
)

func TestMain(m *testing.M) {
	runtime.LockOSThread()

	installMenu()
	mainMenuTopLevelCount = testMainMenuTopLevelCount()
	hasEditMenu = testHasTopLevelMenuTitled("Edit")
	hasAppMenu = testHasTopLevelMenuTitled("fleetdeck")
	editMenuActionKeys = testEditMenuActionKeys()
	quitKey, quitKeyOK = testAppMenuQuitKeyEquivalent()

	closeHideWindow = testNewHiddenWindow()
	if closeHideWindow != nil {
		installCloseToHide(closeHideWindow)
		closeHideShouldCloseResult = windowShouldCloseForTest(closeHideWindow)
		closeHideVisibleAfterClose = testWindowIsVisible(closeHideWindow)
		closeHideAppDelegateSet = testAppDelegateSet()
		closeHideReopenResult = testDispatchReopen(false)
		closeHideVisibleAfterReopen = testWindowIsVisible(closeHideWindow)
	}

	os.Exit(m.Run())
}

func TestInstallMenuBuildsAMainMenuWithAppAndEditMenus(t *testing.T) {
	if mainMenuTopLevelCount < 2 {
		t.Fatalf("main menu has %d top-level items, want at least 2 (app menu, Edit)", mainMenuTopLevelCount)
	}
	if !hasEditMenu {
		t.Fatal("no top-level item titled \"Edit\" -- the menu the whole fix is for is missing")
	}
	if !hasAppMenu {
		t.Fatal("no app menu titled \"fleetdeck\" -- Cmd+Q has nothing to route through without it")
	}
}

// TestAppMenuQuitRoutesToTerminate only proves the menu item exists with
// the standard Cmd+Q key equivalent and the standard terminate: action --
// unmodified NSApplication default behaviour, since nothing in this
// package overrides applicationShouldTerminate:. Whether the app actually
// exits, including with the window hidden, needs a human to press it.
func TestAppMenuQuitRoutesToTerminate(t *testing.T) {
	if !quitKeyOK {
		t.Fatal("app menu has no item with action terminate: -- Cmd+Q has nothing to route through")
	}
	if quitKey != "q" {
		t.Fatalf("Quit key equivalent = %q, want \"q\"", quitKey)
	}
}

func TestInstallMenuEditMenuHasTheFullStandardSet(t *testing.T) {
	// action -> key equivalent. Exactly what the operator found broken by
	// hand: Paste, plus Copy/Cut/Select All by the same mechanism, plus
	// Undo/Redo found afterwards.
	want := map[string]string{
		"undo:":      "z",
		"redo:":      "Z",
		"cut:":       "x",
		"copy:":      "c",
		"paste:":     "v",
		"selectAll:": "a",
	}

	if editMenuActionKeys == nil {
		t.Fatal("no Edit menu to check items in")
	}
	for action, wantKey := range want {
		gotKey, ok := editMenuActionKeys[action]
		if !ok {
			t.Errorf("Edit menu has no item with action %s", action)
			continue
		}
		if gotKey != wantKey {
			t.Errorf("%s: key equivalent %q, want %q", action, gotKey, wantKey)
		}
	}
}

func TestInstallCloseToHideRefusesTheCloseAndHidesTheWindow(t *testing.T) {
	if closeHideWindow == nil {
		t.Fatal("TestMain could not create a test NSWindow")
	}
	if closeHideShouldCloseResult != 0 {
		t.Fatalf("windowShouldClose: returned %d, want 0 (NO) -- a real close would tear the engine down under a still-running panel", closeHideShouldCloseResult)
	}
	if closeHideVisibleAfterClose {
		t.Fatal("window is still visible after windowShouldClose: -- it should have been ordered out")
	}
}

// TestInstallCloseToHideAppDelegateBringsTheWindowBack is the reopen half:
// a Dock icon click with no visible windows must show the same window
// again, not create a second one and not leave it hidden.
func TestInstallCloseToHideAppDelegateBringsTheWindowBack(t *testing.T) {
	if closeHideWindow == nil {
		t.Fatal("TestMain could not create a test NSWindow")
	}
	if !closeHideAppDelegateSet {
		t.Fatal("installCloseToHide did not set an app delegate")
	}
	if !closeHideReopenResult {
		t.Fatal("applicationShouldHandleReopen: returned NO -- Dock click would do nothing")
	}
	if !closeHideVisibleAfterReopen {
		t.Fatal("window is still hidden after applicationShouldHandleReopen: -- Dock click did not bring it back")
	}
}
