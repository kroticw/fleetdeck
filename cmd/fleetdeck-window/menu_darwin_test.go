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
	reloadKey             string
	reloadKeyOK           bool
	reloadHasTarget       bool
	// reloadRuns is how many times pressing Reload ran the window's reload.
	reloadRuns    int
	reloadPressed bool

	closeHideWindow             unsafe.Pointer
	closeHideShouldCloseResult  int
	closeHideVisibleAfterClose  bool
	closeHideAppDelegateSet     bool
	closeHideReopenResult       bool
	closeHideVisibleAfterReopen bool
)

func TestMain(m *testing.M) {
	// A stand-in panel for the takeover tests, started as a panel is
	// (handoverstart_test.go): no AppKit, no tests.
	if addr := os.Getenv(testPanelEnv); addr != "" {
		runTestPanel(addr)
		return
	}
	runtime.LockOSThread()

	installMenu("fleetdeck")
	mainMenuTopLevelCount = testMainMenuTopLevelCount()
	hasEditMenu = testHasTopLevelMenuTitled("Edit")
	hasAppMenu = testHasTopLevelMenuTitled("fleetdeck")
	editMenuActionKeys = testEditMenuActionKeys()
	quitKey, quitKeyOK = testAppMenuQuitKeyEquivalent()
	reloadKey, reloadKeyOK = testMenuItemKey("View", "fleetdeckReloadAll:")
	reloadHasTarget = testMenuItemHasTarget("View", "fleetdeckReloadAll:")
	// Pressed as a click presses it: through the item's target, into what
	// glasswindow.go sets as the reload.
	setMenuReload(func() { reloadRuns++ })
	reloadPressed = testMenuItemPerform("View", "fleetdeckReloadAll:")
	setMenuReload(nil)

	closeHideWindow = testNewHiddenWindow()
	if closeHideWindow != nil {
		installCloseToHide(closeHideWindow)
		closeHideShouldCloseResult = windowShouldCloseForTest(closeHideWindow)
		closeHideVisibleAfterClose = testWindowIsVisible(closeHideWindow)
		closeHideAppDelegateSet = testAppDelegateSet()
		closeHideReopenResult = testDispatchReopen(false)
		closeHideVisibleAfterReopen = testWindowIsVisible(closeHideWindow)
	}

	collectFrameResults()
	collectBandResults()
	collectSurfaceResults()
	collectCapsuleResults()
	collectSystemAppearanceResults()

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

// The page in the window used to live forever: the red button hides the
// window rather than closing it, and there was no way to reload short of
// quitting. With the glass frame the window holds three web views, and
// WKWebView's own reload:, sent up the responder chain, reaches only the one
// with focus -- a reload that left the board on an old build under a new
// orchestrator column. So Reload has a target of the window's own, which
// reloads all three. As with the Edit menu, this proves the item exists with
// the right action, target and key; that Cmd+R reloads every web view for a
// person pressing it is checked on the stand, by hand.
func TestReloadReloadsEveryWebViewNotOnlyTheFocusedOne(t *testing.T) {
	if !reloadKeyOK {
		t.Fatal("no View menu item with action fleetdeckReloadAll: -- Reload would reach only the focused web view")
	}
	if !reloadHasTarget {
		t.Fatal("Reload has no target of its own: its action would go up the responder chain and find nothing")
	}
	if reloadKey != "r" {
		t.Fatalf("Reload key equivalent = %q, want \"r\"", reloadKey)
	}
	if !reloadPressed || reloadRuns != 1 {
		t.Fatalf("pressing Reload: taken = %v, the window's reload ran %d times; want it run once", reloadPressed, reloadRuns)
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
