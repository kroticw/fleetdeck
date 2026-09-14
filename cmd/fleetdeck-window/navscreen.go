//go:build darwin

package main

import (
	"fmt"
	"html"
	"time"
)

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
// So a navigation is asked for again shortly after WebKit says it failed, or
// its web content process went away -- waiting longer mends neither -- and
// otherwise only after a wait that covers the web content process starting:
// from the ask until WebKit commits the navigation, and from the commit on.

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
	// navSilentWait is how long a navigation WebKit has not committed is left
	// before it is asked for again, begun or not: the measured start, three
	// times over. A begun navigation is WebKit's to fail -- it reports a
	// connection that does not come -- and this bound is for one it never
	// reports on. A window whose delegate never reports puts the failure page
	// up after pageLoadTries of these, about 15 s.
	navSilentWait = measuredWebContentStart * pageLoadMargin

	// navFailedRetryPause is how long after a navigation failed before any
	// document came -- a panel not listening for a moment -- it is asked for
	// again: long enough that three tries are not spent within milliseconds.
	navFailedRetryPause = 250 * time.Millisecond

	// maxProcessLossReloads and processLossReset are WebKit's own bound on
	// reloading a page whose web content process went away, which setting a
	// navigation delegate hands to the window (WebPageProxy::
	// tryReloadAfterProcessTermination): maximumWebProcessRelaunchAttempts, one
	// reload, and resetRecentCrashCountDelay, the 30 s after a finished load
	// that clear the count.
	maxProcessLossReloads = 1
	processLossReset      = 30 * time.Second
)

// pageWatch is one web view's page asked for and shown, by WebKit's word
// (nav_darwin.go) and the page's own (pageLoadScript): when it is asked for
// again, and when the window gives up on it. The screen keeps the board's
// (owner.go); the controller keeps one for each side surface, whose web views
// start, fail and lose their process the way the board's does (controller.go).
type pageWatch struct {
	// showingPanel: the panel's own page has said it loaded, with its styles
	// and its scripts, and has not left since.
	showingPanel bool
	// asked: the page is asked for, and has not said it loaded yet. askedAt is
	// when, tries how many asks in a row.
	asked   bool
	askedAt time.Time
	tries   int
	// loading: the navigation asked for has reached its document, which is
	// still loading.
	loading bool
	// href is where the panel's page last said it was; left, that the page
	// went away to an address the window does not know yet.
	href string
	left bool
	// waitFrom is when the wait running now began: the ask, the document
	// beginning, or the failure it waits out (waitFor).
	waitFrom time.Time
	// committed: WebKit has committed a navigation since the page was last
	// asked for, so the page's word is about a document that ask brought;
	// navSeen, that WebKit has said anything at all.
	committed, navSeen bool
	// retrySoon: the page failed, or loaded broken, and is asked for again once
	// navFailedRetryPause has gone by.
	retrySoon bool
	// processLosses counts the web content process going away under the shown
	// page; finishedAt is the last load WebKit finished.
	processLosses int
	finishedAt    time.Time
}

// loadVerdict is what a pageWatch says is to be done.
type loadVerdict int

const (
	// loadGoesOn: nothing, yet.
	loadGoesOn loadVerdict = iota
	// loadAskAgain: the page is to be asked for again, the ask counted.
	loadAskAgain
	// loadFailed: pageLoadTries asks in a row did not load, and the page is
	// given up on.
	loadFailed
	// loadKeepsFalling: the web content process went away again soon after the
	// page was asked for again for the same.
	loadKeepsFalling
)

// ask is the page asked for, counted. It has not begun until WebKit or the
// page says so.
func (p *pageWatch) ask(now time.Time) {
	p.showingPanel, p.loading, p.left = false, false, false
	p.committed, p.retrySoon = false, false
	p.asked, p.askedAt, p.waitFrom = true, now, now
	p.tries++
}

// cover is a page of the window's own going up in place of the page.
func (p *pageWatch) cover() {
	p.showingPanel, p.asked, p.left = false, false, false
	p.committed, p.retrySoon = false, false
}

// navSays takes WebKit's word about a navigation. It says what is to be done,
// and, for a page given up on, how long it was waited for.
func (p *pageWatch) navSays(e navEvent, now time.Time) (loadVerdict, time.Duration) {
	p.navSeen = true
	if e.kind == navFinished && e.href != "about:blank" {
		p.finishedAt = now
	}
	if e.kind == navProcessGone && p.showingPanel {
		return p.processLost(now), 0
	}
	if !p.asked {
		return loadGoesOn, 0
	}
	switch e.kind {
	case navCommitted:
		if e.href != "about:blank" {
			p.committed = true
			if !p.loading {
				p.loading, p.waitFrom = true, now
			}
		}
	case navFailedProvisional, navFailed:
		if !replaced(e) {
			return p.failed(now)
		}
	case navProcessGone:
		return p.failed(now)
	}
	return loadGoesOn, 0
}

// navSays takes WebKit's word about the board's navigation. It says whether to
// ask for the page again now, or the failure page to put up.
func (s *screen) navSays(e navEvent) (navigate bool, page string) {
	return s.follow(s.pageWatch.navSays(e, s.time()))
}

// follow does for the board what its page watch says.
func (s *screen) follow(v loadVerdict, waited time.Duration) (navigate bool, page string) {
	switch v {
	case loadAskAgain:
		return s.ask(), ""
	case loadFailed:
		s.cover()
		return false, pageFailedPage(s.target(), waited)
	case loadKeepsFalling:
		s.cover()
		return false, processLostPage(s.target())
	}
	return false, ""
}

// processLost is the web content process gone under the shown panel. With a
// navigation delegate set, WebKit leaves the page blank rather than reload it
// (NavigationState::NavigationClient::processDidTerminate reports the
// termination handled), so the window asks for the page again -- once, as
// WebKit would: a second loss within processLossReset of the last finished
// load puts up a page saying so, rather than reloading a page that takes its
// process down every time it is shown.
func (p *pageWatch) processLost(now time.Time) loadVerdict {
	if !p.finishedAt.IsZero() && now.Sub(p.finishedAt) >= processLossReset {
		p.processLosses = 0
	}
	p.processLosses++
	if p.processLosses > maxProcessLossReloads {
		// The button asks afresh, with a reload allowed again.
		p.processLosses = 0
		p.cover()
		return loadKeepsFalling
	}
	// Asked for afresh, the way a person's reload is.
	p.tries = 0
	return loadAskAgain
}

// processLostPage says the panel's page at pageURL took its web content process
// down again right after it was reloaded, and offers to ask for it again.
func processLostPage(pageURL string) string {
	return fmt.Sprintf(`<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>fleetdeck</title>
%s
</head>
<body>
<main>
  <h1>Страница панели падает</h1>
  <p>Процесс, который показывает страницу <code>%s</code>, завершился снова вскоре после того, как окно открыло её заново. Окно больше не открывает её само.</p>
  <button id="again">Открыть снова</button>
</main>
<script>
  const again = document.getElementById("again");
  again.addEventListener("click", () => {
    again.disabled = true;
    again.textContent = "Открываю…";
    window.%s();
  });
</script>
</body>
</html>`, pageStyle, html.EscapeString(pageURL), reloadBindingName)
}

// replaced is a navigation that failed because another took its place -- a page
// of the window's put up, the page asked for anew -- not because the page did.
func replaced(e navEvent) bool {
	return (e.errDomain == "NSURLErrorDomain" && e.errCode == nsURLErrorCancelled) ||
		(e.errDomain == "WebKitErrorDomain" && e.errCode == webKitErrorFrameLoadInterrupted)
}

// failed is the page asked for failing: asked for again once
// navFailedRetryPause has gone by, or, past pageLoadTries, given up on.
func (p *pageWatch) failed(now time.Time) (loadVerdict, time.Duration) {
	if p.tries < pageLoadTries {
		p.retrySoon, p.loading, p.waitFrom = true, false, now
		return loadGoesOn, 0
	}
	waited := now.Sub(p.askedAt)
	p.cover()
	return loadFailed, waited
}

// pageSays takes the page's word about itself, and the address it said it
// from. It says whether the word was taken: one from a page no longer on
// screen, or from a document the last ask cut off, is not.
func (p *pageWatch) pageSays(state, href string, now time.Time) bool {
	if !p.asked && !p.showingPanel {
		// A page the window has covered with one of its own, still speaking
		// as it goes: not what is on screen.
		return false
	}
	if p.navSeen && !p.committed {
		// A word from the document the last ask cut off, taken off the UI
		// thread's queue after that ask: WebKit has not yet committed the
		// navigation the window asked for. Without WebKit's word at all -- a
		// delegate that never took -- the page's word is all there is.
		return false
	}
	switch state {
	case pagePanel:
		p.showingPanel, p.asked, p.tries, p.loading, p.left = true, false, 0, false, false
		if href != "" {
			p.href = href
		}
	case pageLoading:
		// The document has begun: given pageLoadingWait from here, and asked
		// for again, if it has to be, where it began.
		if !p.asked {
			return false
		}
		if !p.loading {
			p.loading, p.waitFrom = true, now
		}
		if href != "" {
			p.href = href
		}
		if p.left {
			// The page the person went to: counted from here, as a first try.
			p.left, p.askedAt, p.waitFrom, p.tries = false, now, now, 1
		}
	case pageBroken:
		// Not the panel, and finished: waiting longer mends nothing, so it is
		// asked for again after navFailedRetryPause.
		p.showingPanel, p.loading, p.retrySoon, p.waitFrom = false, false, true, now
	case pageLeaving:
		if p.showingPanel {
			// Gone to an address the window does not know until the next page
			// begins -- a fleet chosen on the start page, a reload. The window
			// waits for that page and does not navigate for it: its own URL is
			// the start page, and asking for it would lose where the person
			// went. Should the next page never begin, it says so.
			p.showingPanel, p.loading = false, false
			p.asked, p.askedAt, p.tries, p.left = true, now, pageLoadTries, true
			p.waitFrom = p.askedAt
		}
	}
	return true
}

// due is time going by: a page asked for and not loaded is asked for again,
// pageLoadTries times in all, and then given up on, having been given the wait
// it says. How long that is follows from what WebKit and the page have said of
// it (waitFor).
func (p *pageWatch) due(now time.Time) (loadVerdict, time.Duration) {
	wait := p.waitFor()
	if !p.asked || now.Sub(p.waitFrom) < wait {
		return loadGoesOn, 0
	}
	if p.tries < pageLoadTries {
		return loadAskAgain, 0
	}
	p.cover()
	return loadFailed, wait
}

// waitFor is how long the page asked for is left, from waitFrom, before due
// asks for it again.
func (p *pageWatch) waitFor() time.Duration {
	switch {
	case p.retrySoon:
		return navFailedRetryPause
	case p.loading || p.left:
		// A document that has begun, or a page the person went to.
		return pageLoadingWait
	default:
		return navSilentWait
	}
}
