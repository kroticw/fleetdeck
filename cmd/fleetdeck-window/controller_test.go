//go:build darwin

package main

import (
	"reflect"
	"testing"
	"time"
)

func started() *controller {
	c := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348}, glassModeGlass)
	c.resized(1512, 982, false)
	return c
}

// loadedFrame is a window whose board reported the panel and whose surfaces both loaded.
func loadedFrame() *controller {
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
	c := loadedFrame()
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
	c := loadedFrame()
	c.pageLoaded("sessions", "leaving")
	for _, e := range c.panel("sessions", true) {
		if msg, ok := e.(sendTo); ok && msg.Surface == "sessions" {
			t.Fatalf("sent to a leaving surface: %#v", msg)
		}
	}
}

// onClock puts c on a clock the test moves.
func onClock(c *controller) *time.Time {
	now := time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return &now
}

// framedOnClock is a window whose board reported the panel, on a clock the test
// moves from the moment the surfaces were asked for.
func framedOnClock() (*controller, *time.Time) {
	c := started()
	clock := onClock(c)
	c.layout(1, "panel", "work")
	return c, clock
}

const surfacePage = "http://127.0.0.1:7777/?fleet=work"

// windowPage is the heading of the page of the window's own effects put up, ""
// for none.
func windowPage(effects []effect) string {
	for _, e := range effects {
		if p, ok := e.(showWindowPage); ok {
			return pageHeading(p.HTML)
		}
	}
	return ""
}

// The same rules as the board's (navscreen_test.go), for the surfaces: their
// web views start with the board's web content process, and a surface that
// gave up on its page at the old 531 ms would take the frame down over a
// panel that answers, as T-059 found for the board on the macos-15 runner.

func TestABrokenSurfaceIsAskedForAgainAfterAPauseAndThenTheWindowSaysSo(t *testing.T) {
	c, clock := framedOnClock()
	for try := 1; try < pageLoadTries; try++ {
		if got := c.pageLoaded("orchestrator", "broken"); len(got) != 0 {
			t.Fatalf("try %d: a broken page: %#v, want nothing yet", try, got)
		}
		brokenAt := *clock
		*clock = brokenAt.Add(100 * time.Millisecond)
		if got := c.tick(); len(got) != 0 {
			t.Fatalf("try %d: tick 100 ms after the broken page: %#v, want nothing yet", try, got)
		}
		*clock = brokenAt.Add(300 * time.Millisecond)
		if got := c.tick(); !reflect.DeepEqual(got, []effect{reloadSurface{Surface: "orchestrator"}}) {
			t.Fatalf("try %d: tick after the pause: %#v, want the orchestrator asked for again", try, got)
		}
	}
	c.pageLoaded("orchestrator", "broken")
	*clock = clock.Add(300 * time.Millisecond)
	got := c.tick()
	if len(got) == 0 || got[0] != (destroySurfaces{}) || windowPage(got) != "Страница панели не загрузилась" {
		t.Fatalf("after %d broken pages: %#v, want the frame down and the failure page", pageLoadTries, got)
	}
}

func TestASurfaceNavigationUnderWayIsNotAskedForAgain(t *testing.T) {
	c, clock := framedOnClock()
	start := *clock
	c.surfaceNavigated("sessions", navEvent{kind: navStarted, href: surfacePage})
	for clock.Sub(start)+pageLoadTick < navSilentWait {
		*clock = clock.Add(pageLoadTick)
		if got := c.tick(); len(got) != 0 {
			t.Fatalf("%s after the surfaces were asked for: %#v, want them left to start", clock.Sub(start), got)
		}
	}
	*clock = start.Add(navSilentWait)
	want := []effect{reloadSurface{Surface: "orchestrator"}, reloadSurface{Surface: "sessions"}}
	if got := c.tick(); !reflect.DeepEqual(got, want) {
		t.Fatalf("at navSilentWait: %#v, want both surfaces asked for again", got)
	}
}

func TestASurfaceNavigationThatFailsIsAskedForAgainAfterAPause(t *testing.T) {
	c, clock := framedOnClock()
	refused := navEvent{kind: navFailedProvisional, href: surfacePage, errDomain: "NSURLErrorDomain", errCode: -1004}
	for try := 1; try < pageLoadTries; try++ {
		if got := c.surfaceNavigated("sessions", refused); len(got) != 0 {
			t.Fatalf("try %d failed: %#v, want nothing yet", try, got)
		}
		failedAt := *clock
		*clock = failedAt.Add(100 * time.Millisecond)
		if got := c.tick(); len(got) != 0 {
			t.Fatalf("try %d: tick 100 ms after the failure: %#v, want nothing yet", try, got)
		}
		*clock = failedAt.Add(300 * time.Millisecond)
		if got := c.tick(); !reflect.DeepEqual(got, []effect{reloadSurface{Surface: "sessions"}}) {
			t.Fatalf("try %d: tick after the pause: %#v, want the sessions surface asked for again", try, got)
		}
	}
	got := c.surfaceNavigated("sessions", refused)
	if len(got) == 0 || got[0] != (destroySurfaces{}) || windowPage(got) != "Страница панели не загрузилась" {
		t.Fatalf("after %d failed navigations: %#v, want the frame down and the failure page", pageLoadTries, got)
	}
}

func TestACancelledOrInterruptedSurfaceNavigationIsNotAFailure(t *testing.T) {
	c, clock := framedOnClock()
	start := *clock
	c.surfaceNavigated("sessions", navEvent{kind: navFailedProvisional, errDomain: "NSURLErrorDomain", errCode: nsURLErrorCancelled})
	c.surfaceNavigated("sessions", navEvent{kind: navFailedProvisional, href: surfacePage, errDomain: "WebKitErrorDomain", errCode: webKitErrorFrameLoadInterrupted})
	*clock = start.Add(time.Second)
	if got := c.tick(); len(got) != 0 {
		t.Fatalf("a second after a replaced navigation: %#v, want nothing", got)
	}
}

func TestASlowlyCommittedSurfaceGetsItsLoadingWaitFromTheCommit(t *testing.T) {
	c, clock := framedOnClock()
	start := *clock
	*clock = start.Add(1200 * time.Millisecond)
	c.surfaceNavigated("sessions", navEvent{kind: navCommitted, href: surfacePage})
	c.surfaceNavigated("orchestrator", navEvent{kind: navCommitted, href: surfacePage})
	*clock = start.Add(1200*time.Millisecond + pageLoadingWait - pageLoadTick)
	if got := c.tick(); len(got) != 0 {
		t.Fatalf("short of pageLoadingWait after the commit: %#v, want the documents left to load", got)
	}
	*clock = start.Add(1200*time.Millisecond + pageLoadingWait)
	if got := c.tick(); len(got) != 2 {
		t.Fatalf("pageLoadingWait after the commit: %#v, want both surfaces asked for again", got)
	}
}

func TestASurfacesWordBeforeWebKitCommitsItsNavigationIsIgnored(t *testing.T) {
	c, _ := framedOnClock()
	c.surfaceNavigated("sessions", navEvent{kind: navStarted, href: surfacePage})
	if got := c.pageLoaded("sessions", "panel"); len(got) != 0 {
		t.Fatalf("the word of a document not yet committed: %#v, want it ignored", got)
	}
	c.surfaceNavigated("sessions", navEvent{kind: navCommitted, href: surfacePage})
	if got := c.pageLoaded("sessions", "panel"); len(got) == 0 {
		t.Fatal("the committed document saying it loaded gets nothing")
	}
}

// surfaceShown is the sessions surface's page loaded, as WebKit and the page say.
func surfaceShown(t *testing.T, c *controller) {
	t.Helper()
	c.surfaceNavigated("sessions", navEvent{kind: navCommitted, href: surfacePage})
	c.surfaceNavigated("sessions", navEvent{kind: navFinished, href: surfacePage})
	if got := c.pageLoaded("sessions", "panel"); len(got) == 0 {
		t.Fatal("the sessions surface's page is not taken as loaded")
	}
}

func TestASurfaceWhoseProcessGoesIsReloadedOnceAndThenTheWindowSaysSo(t *testing.T) {
	c, clock := framedOnClock()
	surfaceShown(t, c)
	if got := c.surfaceNavigated("sessions", navEvent{kind: navProcessGone}); !reflect.DeepEqual(got, []effect{reloadSurface{Surface: "sessions"}}) {
		t.Fatalf("the web content process gone under the shown surface: %#v, want it asked for again", got)
	}
	*clock = clock.Add(time.Second)
	surfaceShown(t, c)
	*clock = clock.Add(time.Second)
	got := c.surfaceNavigated("sessions", navEvent{kind: navProcessGone})
	if len(got) == 0 || got[0] != (destroySurfaces{}) || windowPage(got) != "Страница панели падает" {
		t.Fatalf("a second loss 1 s after the reload loaded: %#v, want the frame down and the page saying the page keeps falling", got)
	}
}

func TestASurfaceProcessLostLongAfterTheLastLossIsReloadedAgain(t *testing.T) {
	c, clock := framedOnClock()
	surfaceShown(t, c)
	c.surfaceNavigated("sessions", navEvent{kind: navProcessGone})
	surfaceShown(t, c)
	*clock = clock.Add(31 * time.Second)
	if got := c.surfaceNavigated("sessions", navEvent{kind: navProcessGone}); !reflect.DeepEqual(got, []effect{reloadSurface{Surface: "sessions"}}) {
		t.Fatalf("a loss 31 s after the reloaded page loaded: %#v, want it asked for again", got)
	}
}

func TestNoSurfaceIsWatchedWithoutAFrame(t *testing.T) {
	c := started()
	onClock(c)
	if got := c.surfaceNavigated("sessions", navEvent{kind: navProcessGone}); len(got) != 0 {
		t.Fatalf("a navigation event with no frame: %#v", got)
	}
	if got := c.tick(); len(got) != 0 {
		t.Fatalf("a tick with no frame: %#v", got)
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
	c := loadedFrame()
	if got := c.boardShowsOwnPage(); !reflect.DeepEqual(got, []effect{destroySurfaces{}}) {
		t.Fatalf("effects = %#v", got)
	}
	if got := c.boardShowsOwnPage(); len(got) != 0 {
		t.Fatalf("second time: %#v, want none", got)
	}
}

func TestOpeningASessionFromASideSurfaceGoesToTheBoardAndFocusesIt(t *testing.T) {
	got := loadedFrame().open("session", "", "abc12345")
	want := []effect{
		sendTo{Surface: "board", Message: map[string]any{"type": "open", "kind": "session", "short": "abc12345"}},
		focusSurface{Surface: "board"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestThePinnedOrchestratorIsFocusedNotOpenedTwice(t *testing.T) {
	got := loadedFrame().open("orchestrator", "", "")
	want := []effect{
		focusSurface{Surface: "orchestrator"},
		sendTo{Surface: "orchestrator", Message: map[string]any{"type": "focusTerminal"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestSwitchingFleetNavigatesTheBoardAndDropsTheSurfaces(t *testing.T) {
	got := loadedFrame().switchFleet("home life")
	want := []effect{destroySurfaces{}, navigateBoard{URL: "http://127.0.0.1:7777/?fleet=home+life"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestASurfaceLinkElsewhereOnThePanelOpensInTheBoard(t *testing.T) {
	allow, got := loadedFrame().navigate("http://127.0.0.1:7777/setup.html")
	want := []effect{destroySurfaces{}, navigateBoard{URL: "http://127.0.0.1:7777/setup.html"}}
	if allow || !reflect.DeepEqual(got, want) {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestASurfaceLinkToAnotherSiteOpensInTheBrowser(t *testing.T) {
	allow, got := loadedFrame().navigate("https://github.com/kroticw/fleetdeck/pull/1")
	if allow || !reflect.DeepEqual(got, []effect{openExternal{URL: "https://github.com/kroticw/fleetdeck/pull/1"}}) {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestASurfaceLoadingItsOwnPageIsLetThrough(t *testing.T) {
	allow, got := loadedFrame().navigate("http://127.0.0.1:7777/?fleet=work")
	if !allow || len(got) != 0 {
		t.Fatalf("allow = %v, effects = %#v", allow, got)
	}
}

func TestFoldingSavesTheWidthsAndTellsTheSurface(t *testing.T) {
	got := loadedFrame().panel("sessions", true)
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
	c := loadedFrame()
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
	c := loadedFrame()
	c.panel("sessions", true)
	if _, ok := c.resizeStart("sessions"); ok {
		t.Fatal("a folded panel is the strip of marks and has no width to drag")
	}
	if got := c.resizeTo("sessions", 348, -40); len(got) != 0 {
		t.Fatalf("a drag on a folded panel: %#v, want none", got)
	}
}

func TestAThemeReportedByTheBoardReachesTheOthersAndTheWindow(t *testing.T) {
	got := loadedFrame().theme("dark")
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
	got := loadedFrame().glassChanged(glassModeOpaque)
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
	c := loadedFrame()
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
	got := loadedFrame().resized(1440, 900, true)
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
	if got := loadedFrame().capsules(model); !reflect.DeepEqual(got, []effect{setCapsules{Model: model}}) {
		t.Fatalf("effects = %#v", got)
	}
}

func TestReloadReloadsEveryWebView(t *testing.T) {
	want := []effect{reloadBoard{}, reloadSurface{Surface: "orchestrator"}, reloadSurface{Surface: "sessions"}}
	if got := loadedFrame().reload(); !reflect.DeepEqual(got, want) {
		t.Fatalf("effects = %#v", got)
	}
	if got := started().reload(); !reflect.DeepEqual(got, []effect{reloadBoard{}}) {
		t.Fatalf("with no frame: %#v, want only the board", got)
	}
}

// A reload is a person's: the surfaces are asked for afresh, their tries
// counted from the start, and nothing is sent to them until they load again.
func TestAReloadAsksForTheSurfacesAfresh(t *testing.T) {
	c := loadedFrame()
	clock := onClock(c)
	c.pageLoaded("sessions", "leaving")
	c.reloadSurfaces()
	for _, e := range c.panel("sessions", true) {
		if msg, ok := e.(sendTo); ok && msg.Surface == "sessions" {
			t.Fatalf("sent to a surface asked for again: %#v", msg)
		}
	}
	for try := 1; try < pageLoadTries; try++ {
		c.pageLoaded("sessions", "broken")
		*clock = clock.Add(300 * time.Millisecond)
		if got := c.tick(); !reflect.DeepEqual(got, []effect{reloadSurface{Surface: "sessions"}}) {
			t.Fatalf("try %d after the reload: %#v, want the surface asked for again, not given up on", try, got)
		}
	}
}
