//go:build darwin

package main

import (
	"strings"
	"testing"

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
	// The measured start, and then some: every tick short of navUnderWayWait.
	for c.t.Sub(s.askedAt)+pageLoadTick < navUnderWayWait {
		c.t = c.t.Add(pageLoadTick)
		if navigate, html := s.tick(); navigate || html != "" {
			t.Fatalf("%s after the navigation began, tick = %v, %q; want it left to go on", c.t.Sub(s.askedAt), navigate, html)
		}
	}
	if c.t.Sub(s.askedAt) <= measuredWebContentStart {
		t.Fatalf("navUnderWayWait %s does not cover the measured start %s", navUnderWayWait, measuredWebContentStart)
	}
}

// WebKit that says a navigation failed before any document came -- a panel
// not listening for a moment, say -- is asked again at once, not after a wait,
// and the failures count towards the failure page.
func TestANavigationThatFailsBeforeItCommitsIsAskedForAgainAtOnce(t *testing.T) {
	s, _ := asked(t)
	for try := 1; try < pageLoadTries; try++ {
		s.navSays(navEvent{kind: navStarted, href: testURL})
		navigate, html := s.navSays(navEvent{kind: navFailedProvisional, href: testURL, errDomain: "NSURLErrorDomain", errCode: -1004})
		if !navigate || html != "" {
			t.Fatalf("try %d failed before it committed: navSays = %v, %q; want the page asked for again", try, navigate, html)
		}
	}
	s.navSays(navEvent{kind: navStarted, href: testURL})
	navigate, html := s.navSays(navEvent{kind: navFailedProvisional, href: testURL, errDomain: "NSURLErrorDomain", errCode: -1004})
	if navigate || !strings.Contains(html, "Страница панели не загрузилась") {
		t.Fatalf("after %d failed navigations, navSays = %v, %q; want the failure page", pageLoadTries, navigate, html)
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

func TestAWebContentProcessGoneIsAskedForAgain(t *testing.T) {
	s, _ := asked(t)
	s.navSays(navEvent{kind: navStarted, href: testURL})
	if navigate, _ := s.navSays(navEvent{kind: navProcessGone}); !navigate {
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
	c.t = c.t.Add(navUnderWayWait)
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
