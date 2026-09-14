//go:build darwin

package main

import (
	"reflect"
	"testing"
)

func started() *controller {
	c := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348}, glassModeGlass)
	c.resized(1512, 982, false)
	return c
}

// shown is a window whose board reported the panel and whose surfaces both loaded.
func shown() *controller {
	c := started()
	c.layout(1, "panel", "work")
	c.pageLoaded("orchestrator", "panel")
	c.pageLoaded("sessions", "panel")
	return c
}

func boardInsets() sendTo {
	return sendTo{Surface: "board", Message: map[string]any{"type": "insets", "top": 64.0, "left": 394.0, "right": 0.0, "contentRight": 356.0}}
}

func TestAPageThatReportsTheCurrentVersionGetsItsSurfaces(t *testing.T) {
	got := started().layout(1, "panel", "work")
	want := []effect{
		createSurfaces{Fleet: "work", URL: "http://127.0.0.1:7777/?fleet=work", Glass: glassModeGlass},
		applyGeometry{G: layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})},
		boardInsets(),
		sendTo{Surface: "board", Message: map[string]any{"type": "glass", "glass": "glass"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v\nwant      %#v", got, want)
	}
}

func TestAPageOfAnUnknownVersionStaysOnePlainWebView(t *testing.T) {
	if got := started().layout(2, "panel", "work"); len(got) != 0 {
		t.Fatalf("effects = %#v, want none", got)
	}
}

func TestABoardPageThatIsNotAPanelTakesTheFrameDown(t *testing.T) {
	c := shown()
	if got := c.layout(1, "setup", ""); !reflect.DeepEqual(got, []effect{destroySurfaces{}}) {
		t.Fatalf("effects = %#v", got)
	}
	if got := c.layout(1, "setup", ""); len(got) != 0 {
		t.Fatalf("second time: %#v, want none", got)
	}
}

func TestNothingIsSentToASurfaceBeforeItSaysItLoaded(t *testing.T) {
	c := started()
	c.layout(1, "panel", "work")
	if got := c.theme("dark"); !reflect.DeepEqual(got, []effect{setAppearance{Choice: "dark"}}) {
		t.Fatalf("before the surfaces loaded: %#v, want only the window's appearance", got)
	}
	if got := c.pageLoaded("sessions", "loading"); len(got) != 0 {
		t.Fatalf("a document begun is not a page loaded: %#v", got)
	}
	got := c.pageLoaded("sessions", "panel")
	want := []effect{
		sendTo{Surface: "sessions", Message: map[string]any{"type": "theme", "choice": "dark"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "glass", "glass": "glass"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "folded", "folded": false}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("on load: %#v", got)
	}
}

func TestALeavingSurfaceGetsNothingUntilItLoadsAgain(t *testing.T) {
	c := shown()
	c.pageLoaded("sessions", "leaving")
	for _, e := range c.panel("sessions", true) {
		if msg, ok := e.(sendTo); ok && msg.Surface == "sessions" {
			t.Fatalf("sent to a leaving surface: %#v", msg)
		}
	}
}

func TestABrokenSurfaceIsLoadedAgainAndThenTheWindowSaysSo(t *testing.T) {
	c := started()
	c.layout(1, "panel", "work")
	for try := 1; try < pageLoadTries; try++ {
		if got := c.pageLoaded("orchestrator", "broken"); !reflect.DeepEqual(got, []effect{reloadSurface{Surface: "orchestrator"}}) {
			t.Fatalf("try %d: %#v", try, got)
		}
	}
	if got := c.pageLoaded("orchestrator", "broken"); !reflect.DeepEqual(got, []effect{destroySurfaces{}, showFailedPage{}}) {
		t.Fatalf("last try: %#v", got)
	}
}

func TestTheTakeoverSequenceCreatesSurfacesOnce(t *testing.T) {
	c := started()
	creates := 0
	count := func(effects []effect) []effect {
		for _, e := range effects {
			if _, ok := e.(createSurfaces); ok {
				creates++
			}
		}
		return effects
	}
	// The new window's "taking over" page, then the staged panel answering while
	// the handover is not done: the window's own page both times.
	count(c.boardShowsOwnPage())
	count(c.boardShowsOwnPage())
	// Handed over: the board loads and reports, then loads again once the panel
	// is restarted from the canonical bundle.
	count(c.layout(1, "panel", "work"))
	again := count(c.layout(1, "panel", "work"))
	if creates != 1 {
		t.Fatalf("surfaces created %d times, want once", creates)
	}
	if len(again) == 0 || !reflect.DeepEqual(again[0], boardInsets()) {
		t.Fatalf("a repeated report must give the new board page its insets again: %#v", again)
	}
	count(c.layout(1, "panel", "home"))
	if creates != 2 {
		t.Fatalf("another fleet must bring the surfaces back on it: created %d times", creates)
	}
}

func TestTheWindowsOwnPageTakesTheSurfacesDown(t *testing.T) {
	c := shown()
	if got := c.boardShowsOwnPage(); !reflect.DeepEqual(got, []effect{destroySurfaces{}}) {
		t.Fatalf("effects = %#v", got)
	}
	if got := c.boardShowsOwnPage(); len(got) != 0 {
		t.Fatalf("second time: %#v, want none", got)
	}
}

func TestOpeningASessionFromASideSurfaceGoesToTheBoardAndFocusesIt(t *testing.T) {
	got := shown().open("session", "", "abc12345")
	want := []effect{
		sendTo{Surface: "board", Message: map[string]any{"type": "open", "kind": "session", "short": "abc12345"}},
		focusSurface{Surface: "board"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestThePinnedOrchestratorIsFocusedNotOpenedTwice(t *testing.T) {
	got := shown().open("orchestrator", "", "")
	want := []effect{
		focusSurface{Surface: "orchestrator"},
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "focusTerminal"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestSwitchingFleetNavigatesTheBoardAndDropsTheSurfaces(t *testing.T) {
	got := shown().switchFleet("home life")
	want := []effect{destroySurfaces{}, navigateBoard{URL: "http://127.0.0.1:7777/?fleet=home+life"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestASurfaceLinkElsewhereOnThePanelOpensInTheBoard(t *testing.T) {
	allow, got := shown().navigate("http://127.0.0.1:7777/setup.html")
	want := []effect{destroySurfaces{}, navigateBoard{URL: "http://127.0.0.1:7777/setup.html"}}
	if allow || !reflect.DeepEqual(got, want) {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestASurfaceLinkToAnotherSiteOpensInTheBrowser(t *testing.T) {
	allow, got := shown().navigate("https://github.com/kroticw/fleetdeck/pull/1")
	if allow || !reflect.DeepEqual(got, []effect{openExternal{URL: "https://github.com/kroticw/fleetdeck/pull/1"}}) {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestASurfaceLoadingItsOwnPageIsLetThrough(t *testing.T) {
	allow, got := shown().navigate("http://127.0.0.1:7777/?fleet=work")
	if !allow || len(got) != 0 {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestFoldingSavesTheWidthsAndTellsTheSurface(t *testing.T) {
	got := shown().panel("sessions", true)
	w := panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true}
	want := []effect{
		saveWidths{W: w},
		applyGeometry{G: layoutFor(1512, 982, w)},
		sendTo{Surface: "board", Message: map[string]any{"type": "insets", "top": 64.0, "left": 394.0, "right": 0.0, "contentRight": 56.0}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "folded", "folded": true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestDraggingAPanelsEdgeMovesTheFrameAndGivesTheBoardItsInsetsOnRelease(t *testing.T) {
	c := shown()
	start, ok := c.resizeStart("orchestrator")
	if !ok || start != 368 {
		t.Fatalf("resizeStart = %v, %v; want the panel's width", start, ok)
	}
	w := panelWidths{Orchestrator: 408, Sessions: 348}
	got := c.resizeTo("orchestrator", start, 40)
	if !reflect.DeepEqual(got, []effect{applyGeometry{G: layoutFor(1512, 982, w)}}) {
		t.Fatalf("during the drag: %#v; want only the frame laid out again", got)
	}
	got = c.resizeEnd()
	want := []effect{
		saveWidths{W: w},
		sendTo{Surface: "board", Message: map[string]any{"type": "insets", "top": 64.0, "left": 434.0, "right": 0.0, "contentRight": 356.0}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("on release: %#v", got)
	}
	if got := c.resizeEnd(); len(got) != 0 {
		t.Fatalf("a release with no drag: %#v, want none", got)
	}
}

func TestAFoldedPanelHasNoEdgeToDrag(t *testing.T) {
	c := shown()
	c.panel("sessions", true)
	if _, ok := c.resizeStart("sessions"); ok {
		t.Fatal("a folded panel is the strip of marks and has no width to drag")
	}
	if got := c.resizeTo("sessions", 348, -40); len(got) != 0 {
		t.Fatalf("a drag on a folded panel: %#v, want none", got)
	}
}

func TestAThemeReportedByTheBoardReachesTheOthersAndTheWindow(t *testing.T) {
	got := shown().theme("dark")
	want := []effect{
		setAppearance{Choice: "dark"},
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "theme", "choice": "dark"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "theme", "choice": "dark"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestReducedTransparencyRebuildsTheFrameAndTellsEverySurface(t *testing.T) {
	got := shown().glassChanged(glassModeOpaque)
	want := []effect{
		setFrameMode{Mode: glassModeOpaque},
		sendTo{Surface: "board", Message: map[string]any{"type": "glass", "glass": "opaque"}},
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "glass", "glass": "opaque"}},
		sendTo{Surface: "sessions", Message: map[string]any{"type": "glass", "glass": "opaque"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestACapsulePressBecomesAMessageToTheBoard(t *testing.T) {
	c := shown()
	cases := map[string]map[string]any{
		"tab:docs": {"type": "show", "section": "docs"},
		"newCard":  {"type": "newCard"},
		"theme":    {"type": "cycleTheme"},
	}
	for action, msg := range cases {
		if got := c.capsuleAction(action); !reflect.DeepEqual(got, []effect{sendTo{Surface: "board", Message: msg}}) {
			t.Fatalf("%s: effects = %#v", action, got)
		}
	}
}

func TestFullScreenTellsTheOrchestratorSurfaceToDropTheButtonsRoom(t *testing.T) {
	got := shown().resized(1440, 900, true)
	if got[0] != (applyGeometry{G: layoutFor(1440, 900, panelWidths{Orchestrator: 368, Sessions: 348})}) {
		t.Fatalf("the frame is not laid out for the new size: %#v", got[0])
	}
	last := got[len(got)-1]
	if !reflect.DeepEqual(last, sendTo{Surface: "orchestrator", Message: map[string]any{"type": "fullscreen", "on": true}}) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestTheCapsuleModelIsDrawnAsTheBoardGaveIt(t *testing.T) {
	model := []byte(`{"version":1}`)
	if got := shown().capsules(model); !reflect.DeepEqual(got, []effect{setCapsules{Model: model}}) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestReloadReloadsEveryWebView(t *testing.T) {
	if got := shown().reload(); !reflect.DeepEqual(got, []effect{reloadAll{}}) {
		t.Fatalf("effects = %#v", got)
	}
}
