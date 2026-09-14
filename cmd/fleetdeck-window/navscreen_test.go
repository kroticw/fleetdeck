//go:build darwin

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// On 2026-09-14 the operator's update asked for the panel's page three times
// after done, the third ask answering 261 ms later: pageLoadTries is three,
// and a slower third would have covered a working panel with the failure page.
// The first two asks were each followed by the next 531 ms on, before the
// page could say anything -- whether WebKit had begun them or not, nothing
// said. Now WebKit says (nav_darwin.go), and the window asks again when WebKit
// reports a navigation failed or its web content process gone, not because
// time went by while a navigation was under way.

func asked(t *testing.T) (*screen, *screenClock) {
	t.Helper()
	s, c := newScreen(false)
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1}); !navigate {
		t.Fatal("the panel answering is not opened")
	}
	return s, c
}

// The operator's first two asks.
func TestANavigationUnderWayIsNotAskedForAgain(t *testing.T) {
	s, c := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	// The measured start, and then some: every tick short of navSilentWait.
	for c.t.Sub(s.askedAt)+pageLoadTick < navSilentWait {
		c.t = c.t.Add(pageLoadTick)
		if navigate, html := s.tick(); navigate || html != "" {
			t.Fatalf("%s after the navigation began, tick = %v, %q; want it left to go on", c.t.Sub(s.askedAt), navigate, html)
		}
	}
	if c.t.Sub(s.askedAt) <= measuredWebContentStart {
		t.Fatalf("navSilentWait %s does not cover the measured start %s", navSilentWait, measuredWebContentStart)
	}
}

// WebKit that says a navigation failed before any document came -- a panel
// not listening for a moment, say -- is asked again after navFailedRetryPause,
// not within the same millisecond, and the failures count towards the failure
// page.
func TestANavigationThatFailsBeforeItCommitsIsAskedForAgainAfterAPause(t *testing.T) {
	s, c := asked(t)
	refused := navEvent{kind: navFailedProvisional, href: testURL, errDomain: "NSURLErrorDomain", errCode: -1004}
	for try := 1; try < pageLoadTries; try++ {
		s.navSays(navEvent{kind: navStarted, href: testURL})
		if navigate, html := s.navSays(refused); navigate || html != "" {
			t.Fatalf("try %d failed before it committed: navSays = %v, %q; want nothing yet", try, navigate, html)
		}
		// Fixed times, not ones worked out from navFailedRetryPause: a test
		// that derives them passes with no pause at all.
		failedAt := c.t
		c.t = failedAt.Add(100 * time.Millisecond)
		if navigate, html := s.tick(); navigate || html != "" {
			t.Fatalf("try %d: tick 100 ms after the failure = %v, %q; want nothing yet", try, navigate, html)
		}
		c.t = failedAt.Add(300 * time.Millisecond)
		if navigate, html := s.tick(); !navigate || html != "" {
			t.Fatalf("try %d: tick after the pause = %v, %q; want the page asked for again", try, navigate, html)
		}
	}
	s.navSays(navEvent{kind: navStarted, href: testURL})
	navigate, html := s.navSays(refused)
	if navigate || !strings.Contains(html, "Страница панели не загрузилась") {
		t.Fatalf("after %d failed navigations, navSays = %v, %q; want the failure page", pageLoadTries, navigate, html)
	}
}

// On GitHub's macos-15 runner (PR #166, window-on-oldest-macos, 2026-09-14)
// the board's first document said it was the panel 952 ms after WebKit
// committed it. pageLoadingWait, 840 ms then, ran out first: the window asked
// for the page again and cut off a document that was loading.
func TestADocumentThatTakesNearlyASecondFromCommitToPanelIsNotAskedForAgain(t *testing.T) {
	s, c := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	committed := c.t
	s.navSays(navEvent{kind: navCommitted, href: testURL})
	s.pageSays(pageLoading, testURL)
	for c.t.Sub(committed) < 950*time.Millisecond {
		c.t = c.t.Add(pageLoadTick)
		if navigate, html := s.tick(); navigate || html != "" {
			t.Fatalf("tick %s after the commit = %v, %q; want the document left to load", c.t.Sub(committed), navigate, html)
		}
	}
	s.pageSays(pagePanel, testURL)
	if !s.showingPanel || s.asked {
		t.Fatalf("the document's word 950 ms after its commit left showing = %v, asked = %v; want the panel shown", s.showingPanel, s.asked)
	}
}

// The reviewer's probe: WebKit confirms the navigation 1.2 s after it was asked
// for -- the runner took up to 1.69 s -- and the document gets pageLoadingWait
// from there, not from the ask.
func TestASlowlyCommittedNavigationGetsItsLoadingWaitFromTheCommit(t *testing.T) {
	s, c := asked(t)
	start := c.t
	c.t = start.Add(1100 * time.Millisecond)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	c.t = start.Add(1200 * time.Millisecond)
	s.navSays(navEvent{kind: navCommitted, href: testURL})
	s.pageSays(pageLoading, testURL)
	c.t = start.Add(1300 * time.Millisecond)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick 100 ms after the commit = %v, %q; want the document left to load", navigate, html)
	}
	c.t = start.Add(1200*time.Millisecond + pageLoadingWait - pageLoadTick)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick short of pageLoadingWait after the commit = %v, %q; want it left to load", navigate, html)
	}
	c.t = start.Add(1200*time.Millisecond + pageLoadingWait)
	if navigate, _ := s.tick(); !navigate {
		t.Fatal("a document that never finished within pageLoadingWait of its commit is not asked for again")
	}
}

// The page's word goes through the UI thread's queue, and the window may have
// asked for the page again before the last document's word is taken: that word
// is about a document the new ask has already cut off.
func TestALateWordFromTheDocumentBeforeTheLastAskIsIgnored(t *testing.T) {
	s, _ := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	s.navSays(navEvent{kind: navCommitted, href: testURL})
	// A panel answering again: asked for anew.
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 2})
	s.pageSays(pagePanel, testURL)
	if s.showingPanel || !s.asked {
		t.Fatalf("the cut-off document's late word left showing = %v, asked = %v; want the new ask still waited on", s.showingPanel, s.asked)
	}
	s.navSays(navEvent{kind: navStarted, href: testURL})
	s.navSays(navEvent{kind: navCommitted, href: testURL})
	s.pageSays(pagePanel, testURL)
	if !s.showingPanel || s.asked {
		t.Fatalf("the new document's word left showing = %v, asked = %v", s.showingPanel, s.asked)
	}
}

// Without a word from WebKit at all -- a delegate that never took -- the page's
// own word is taken as it was before there was one.
func TestWithoutWebKitsWordThePagesWordIsTaken(t *testing.T) {
	s, _ := asked(t)
	s.pageSays(pagePanel, testURL)
	if !s.showingPanel {
		t.Fatal("with no navigation events ever, the page saying it loaded is not taken")
	}
}

// With a navigation delegate set, WebKit leaves a page whose web content process
// went away blank rather than reloading it (NavigationState::NavigationClient::
// processDidTerminate): the window asks for it again, showing or not.
func TestAWebContentProcessGoneUnderAShownPanelAsksForItAgain(t *testing.T) {
	s, _ := asked(t)
	s.navSays(navEvent{kind: navCommitted, href: testURL})
	s.pageSays(pagePanel, testURL)
	if navigate, html := s.navSays(navEvent{kind: navProcessGone}); !navigate || html != "" {
		t.Fatalf("the web content process went away under the shown panel: navSays = %v, %q; want the page asked for again", navigate, html)
	}
	if !s.asked || s.tries != 1 {
		t.Fatalf("after asking again, asked = %v, tries = %d; want a first try", s.asked, s.tries)
	}
}

// shown is the panel's page loaded and shown, as WebKit and the page say it.
func shown(t *testing.T, s *screen) {
	t.Helper()
	s.navSays(navEvent{kind: navStarted, href: testURL})
	s.navSays(navEvent{kind: navCommitted, href: testURL})
	s.navSays(navEvent{kind: navFinished, href: testURL})
	s.pageSays(pagePanel, testURL)
	if !s.showingPanel {
		t.Fatal("the panel's page is not taken as shown")
	}
}

// WebKit reloads a page whose web content process went away once, and gives up
// on a second loss within 30 s of the last finished load (WebPageProxy::
// tryReloadAfterProcessTermination, maximumWebProcessRelaunchAttempts,
// resetRecentCrashCountDelay). The window, which does the reloading now, stops
// at the same point and says so, rather than reloading a page that takes its
// process down every time it is shown.
func TestAWebContentProcessLostAgainSoonAfterAReloadPutsUpTheFailurePage(t *testing.T) {
	s, c := asked(t)
	shown(t, s)
	if navigate, _ := s.navSays(navEvent{kind: navProcessGone}); !navigate {
		t.Fatal("the first loss of the web content process is not reloaded")
	}
	c.t = c.t.Add(time.Second)
	shown(t, s)
	c.t = c.t.Add(time.Second)
	navigate, html := s.navSays(navEvent{kind: navProcessGone})
	if navigate || pageHeading(html) != "Страница панели падает" {
		t.Fatalf("a second loss 1 s after the reloaded page loaded: navSays = %v, %q; want the page saying the panel's page keeps going down", navigate, pageHeading(html))
	}
	for i := 0; i < 5; i++ {
		if navigate, html := s.navSays(navEvent{kind: navProcessGone}); navigate || html != "" {
			t.Fatalf("a further loss with the failure page up: navSays = %v, %q; want nothing", navigate, html)
		}
	}
}

// A loss long after the last one is reloaded again, as WebKit would: its count
// is reset 30 s after a load finishes.
func TestAWebContentProcessLostLongAfterTheLastLossIsReloadedAgain(t *testing.T) {
	s, c := asked(t)
	shown(t, s)
	s.navSays(navEvent{kind: navProcessGone})
	shown(t, s)
	c.t = c.t.Add(31 * time.Second)
	if navigate, _ := s.navSays(navEvent{kind: navProcessGone}); !navigate {
		t.Fatal("a loss 31 s after the reloaded page loaded is not reloaded")
	}
}

// A navigation replaced by the window's own next one -- a page of the
// window's put up, a navigation asked for again -- is reported cancelled. That
// is not the page failing.
func TestACancelledNavigationIsNotTakenForAFailure(t *testing.T) {
	s, _ := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	if navigate, html := s.navSays(navEvent{kind: navFailedProvisional, errDomain: "NSURLErrorDomain", errCode: nsURLErrorCancelled}); navigate || html != "" {
		t.Fatalf("a cancelled navigation: navSays = %v, %q; want nothing", navigate, html)
	}
}

// On the runner a navigation replaced by the next was also reported as
// WebKitErrorDomain 102, the frame load interrupted, rather than cancelled.
func TestAnInterruptedNavigationIsNotTakenForAFailure(t *testing.T) {
	s, _ := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	if navigate, html := s.navSays(navEvent{kind: navFailedProvisional, href: testURL, errDomain: "WebKitErrorDomain", errCode: webKitErrorFrameLoadInterrupted}); navigate || html != "" {
		t.Fatalf("an interrupted navigation: navSays = %v, %q; want nothing", navigate, html)
	}
}

func TestAWebContentProcessGoneIsAskedForAgainAfterAPause(t *testing.T) {
	s, c := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	if navigate, html := s.navSays(navEvent{kind: navProcessGone}); navigate || html != "" {
		t.Fatalf("the web content process went away under the page asked for: navSays = %v, %q; want nothing yet", navigate, html)
	}
	c.t = c.t.Add(navFailedRetryPause)
	if navigate, _ := s.tick(); !navigate {
		t.Fatal("the web content process went away under the page asked for, and it is not asked for again")
	}
}

// A committed navigation has its document: it is loading, as the page says
// once its script runs.
func TestACommittedNavigationIsLoading(t *testing.T) {
	s, c := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	s.navSays(navEvent{kind: navCommitted, href: testURL})
	c.t = c.t.Add(pageLoadingWait - pageLoadTick)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick short of pageLoadingWait over a committed navigation = %v, %q; want it left to load", navigate, html)
	}
	c.t = s.askedAt.Add(pageLoadingWait)
	if navigate, _ := s.tick(); !navigate {
		t.Fatal("a committed navigation that never finished loading is not asked for again after pageLoadingWait")
	}
}

// Events about navigations the window did not ask for -- its own pages, put
// up with SetHtml -- say nothing about the panel's page.
func TestNavigationEventsWithNothingAskedForAreIgnored(t *testing.T) {
	s, c := newScreen(false)
	if navigate, html := s.navSays(navEvent{kind: navFailedProvisional, href: "about:blank", errDomain: "NSURLErrorDomain", errCode: -1004}); navigate || html != "" {
		t.Fatalf("navSays with nothing asked for = %v, %q", navigate, html)
	}
	c.t = c.t.Add(navSilentWait)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick with nothing asked for = %v, %q", navigate, html)
	}
}

// A navigation WebKit reports nothing of at all is still asked for again --
// but after navSilentWait: a window just started was seen to wait
// measuredWebContentStart for its web content process, and then load.
func TestANavigationWebKitSaysNothingOfIsAskedForAgainOnlyAfterItsWait(t *testing.T) {
	s, c := asked(t)
	c.t = c.t.Add(measuredWebContentStart)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick at the measured start with no word from WebKit = %v, %q; want it left alone", navigate, html)
	}
	c.t = s.askedAt.Add(navSilentWait)
	if navigate, _ := s.tick(); !navigate {
		t.Fatal("a navigation WebKit says nothing of is never asked for again")
	}
}
