//go:build darwin

package main

import "time"

// The screen taking WebKit's word about navigations (nav_darwin.go).
//
// Until 2026-09-14 the window asked for the panel's page again whenever
// pageLoadWait (531 ms) went by without the page saying it had begun, three
// times in all, and then put up the failure page. A window just started asks
// while its web content process is still starting, and that start was longer
// than those waits could be relied on to cover:
//
//   - The operator's update that day asked at 13:24:20.101, 20.655 and 21.254.
//     The network process had the connection to the panel within 3 ms; the web
//     content process, launched 330 ms before the first ask, answered none of
//     the three navigations' policy checks until 21.480, and then all three at
//     once: two as superseded, the third to load. The third loaded, where one
//     ask would have loaded at the same moment.
//   - On GitHub's macos-15 runner, in the window-on-oldest-macos job the same
//     day, two runs of three put the failure page up over a panel that
//     answered. In twenty runs with WebKit's events logged, WebKit committed
//     the first navigation 0.20-1.69 s after the first ask, and the failure
//     page was due at about 1.65-1.75 s: the run at 1.69 s put it up. The
//     system log of another shows the web content process busy from its
//     launch to its first policy answer, 1.45 s after the first ask, with no
//     pause over 0.21 s: starting, not hung.
//
// And each ask past the first cut off the navigation before it, on the runner
// some of them after that navigation had committed.
//
// So a navigation is asked for again when WebKit says it failed, or its web
// content process went away -- at once, since waiting mends neither -- and
// otherwise only after a wait that covers the web content process starting.

const (
	// nsURLErrorCancelled is NSURLErrorCancelled: a navigation replaced by the
	// window's next one, not a page that failed.
	nsURLErrorCancelled = -999
	// webKitErrorFrameLoadInterrupted is WebKitErrorFrameLoadInterruptedByPolicyChange:
	// on the runner, the same replacement reported by WebKit rather than the
	// URL loading system.
	webKitErrorFrameLoadInterrupted = 102

	// measuredWebContentStart is the longest a window just started was seen to
	// wait for its web content process: from the first ask to WebKit
	// committing the navigation, 1.69 s on the runner, where 20 runs took
	// 0.20-1.69 s; 1.38 s to the policy answer on the operator's update, and
	// 1.57 s to the navigation starting on a stand on the operator's machine.
	measuredWebContentStart = 1693 * time.Millisecond
	// navSilentWait is how long a navigation WebKit says nothing of is left
	// before it is asked for again, and navUnderWayWait one WebKit has begun:
	// the measured start, three times over. A begun navigation is WebKit's to
	// fail -- it reports a connection that does not come -- and this bound is
	// only for one it never reports on.
	navSilentWait   = measuredWebContentStart * pageLoadMargin
	navUnderWayWait = navSilentWait
)

// navSays takes WebKit's word about a navigation. It says whether to ask for
// the page again now, or the failure page to put up.
func (s *screen) navSays(e navEvent) (navigate bool, page string) {
	if !s.asked {
		return false, ""
	}
	switch e.kind {
	case navStarted:
		if e.href != "about:blank" {
			s.underWay = true
		}
	case navCommitted:
		if e.href != "about:blank" {
			s.underWay, s.loading = false, true
		}
	case navFinished:
		s.underWay = false
	case navFailedProvisional, navFailed:
		if replaced(e) {
			return false, ""
		}
		return s.again()
	case navProcessGone:
		return s.again()
	}
	return false, ""
}

// replaced is a navigation that failed because another took its place -- a page
// of the window's put up, the page asked for anew -- not because the page did.
func replaced(e navEvent) bool {
	return (e.errDomain == "NSURLErrorDomain" && e.errCode == nsURLErrorCancelled) ||
		(e.errDomain == "WebKitErrorDomain" && e.errCode == webKitErrorFrameLoadInterrupted)
}

// again asks for the page once more, or, past pageLoadTries, puts up the
// failure page.
func (s *screen) again() (navigate bool, page string) {
	if s.tries < pageLoadTries {
		return s.ask(), ""
	}
	target, waited := s.target(), s.time().Sub(s.askedAt)
	s.cover()
	return false, pageFailedPage(target, waited)
}

// waitFor is how long the page asked for is left before tick asks for it
// again.
func (s *screen) waitFor() time.Duration {
	switch {
	case s.retryNow:
		return 0
	case s.loading || s.left:
		// A document that has begun, or a page the person went to.
		return pageLoadingWait
	case s.underWay:
		return navUnderWayWait
	default:
		return navSilentWait
	}
}
