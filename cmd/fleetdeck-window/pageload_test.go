//go:build darwin

package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
	"github.com/kroticw/fleetdeck/web"
)

// A navigation is a request, not a page. On 2026-09-14 the window took the
// panel's first answer for its page being shown, and the update restarted
// that panel a moment later, while the page was still loading: one window was
// left white, with the document and none of its styles or scripts, and the
// next was left on "Принимаю пульт", its navigation refused before anything
// arrived. The page now says when it has loaded, and until it has, the window
// keeps asking for it.

// screenClock is a screen's time, moved by hand.
type screenClock struct{ t time.Time }

func (c *screenClock) now() time.Time { return c.t }

func newScreen(takingOver bool) (*screen, *screenClock) {
	c := &screenClock{t: time.Date(2026, 9, 14, 10, 51, 44, 0, time.UTC)}
	return &screen{url: testURL, logPath: "/log", takingOver: takingOver, now: c.now}, c
}

// The new window of an update does not open the panel's page while the
// handover is still restarting that panel: the page it asked for would be
// cut off under it. It opens it once, when the handover is done.
func TestTheNewWindowOpensThePanelOnlyOnceTheHandoverIsDone(t *testing.T) {
	s, _ := newScreen(true)
	for _, e := range []supervisor.Event{
		{State: supervisor.Starting, PID: 7439},
		{State: supervisor.Answering, Ours: true, PID: 7439},
		{State: supervisor.Starting, PID: 7449},
		{State: supervisor.Answering, Ours: true, PID: 7449},
	} {
		navigate, html := s.on(e)
		if navigate {
			t.Fatalf("on(%v) during the handover navigates to the panel; want the handover page kept", e.State)
		}
		if e.State == supervisor.Answering && html != "" {
			t.Fatalf("on(%v) during the handover replaces the page: %q", e.State, html)
		}
	}
	if !s.handedOver() {
		t.Fatal("the handover is done and the panel answers, and the window does not open the panel's page")
	}
}

// Done before the panel's answer reached the screen: the answer opens it.
func TestTheNewWindowOpensThePanelWhenItAnswersAfterTheHandoverIsDone(t *testing.T) {
	s, _ := newScreen(true)
	s.on(supervisor.Event{State: supervisor.Starting, PID: 7449})
	if s.handedOver() {
		t.Fatal("handedOver navigates with no panel answering yet")
	}
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 7449}); !navigate {
		t.Fatal("after the handover, the panel answering is not opened")
	}
}

// The handover page, once up, stays up through the handover's second start:
// covering it again would start its count of seconds from zero.
func TestTheHandoverPageIsNotPutUpAgainForTheSecondStart(t *testing.T) {
	s, _ := newScreen(true)
	if _, html := s.on(supervisor.Event{State: supervisor.Starting, PID: 7439}); !strings.Contains(html, "Принимаю пульт") {
		t.Fatalf("the first start shows %q, want the handover page", html)
	}
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 7439})
	if _, html := s.on(supervisor.Event{State: supervisor.Starting, PID: 7449}); html != "" {
		t.Fatalf("the second start puts a page up again: %q", html)
	}
}

// The white window: the page was asked for, the panel was restarted before
// it had loaded, and the restarted panel's answer was taken for a page
// already there.
func TestAPageNotYetLoadedIsOpenedAgainWhenThePanelRestarts(t *testing.T) {
	s, _ := newScreen(false)
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1}); !navigate {
		t.Fatal("the first answer is not opened")
	}
	s.on(supervisor.Event{State: supervisor.Starting, PID: 2})
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 2}); !navigate {
		t.Fatal("the panel restarted before its page said it had loaded, and the window does not ask for the page again")
	}
}

// A page that has loaded reconnects to a restarted panel by itself, keeping
// what the person had open: it is not reloaded under them.
func TestALoadedPageIsLeftAloneWhenThePanelRestarts(t *testing.T) {
	s, _ := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.pageSays(pagePanel, testURL)
	if navigate, html := s.on(supervisor.Event{State: supervisor.Starting, PID: 2}); navigate || html != "" {
		t.Fatalf("on(Starting) over a loaded page = %v, %q; want nothing", navigate, html)
	}
	if navigate, html := s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 2}); navigate || html != "" {
		t.Fatalf("on(Answering) over a loaded page = %v, %q; want nothing", navigate, html)
	}
}

// "Прошло 65 с": a navigation refused before anything arrived leaves the page
// before it on screen, and no event follows to ask again. Time does.
func TestAPageThatDoesNotLoadIsAskedForAgainAndThenSaidToHaveFailed(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	c.t = c.t.Add(navSilentWait - time.Millisecond)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick before the wait is over = %v, %q; want nothing", navigate, html)
	}
	for try := 2; try <= pageLoadTries; try++ {
		c.t = c.t.Add(navSilentWait)
		if navigate, html := s.tick(); !navigate || html != "" {
			t.Fatalf("try %d: tick after the wait = %v, %q; want the page asked for again", try, navigate, html)
		}
	}
	c.t = c.t.Add(navSilentWait)
	navigate, html := s.tick()
	if navigate || !strings.Contains(html, "Страница панели не загрузилась") || !strings.Contains(html, testURL) {
		t.Fatalf("after %d tries, tick = %v, %q; want a page saying the panel's page did not load", pageLoadTries, navigate, html)
	}
	// Said once, not again at every tick.
	c.t = c.t.Add(navSilentWait)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick after the failure was shown = %v, %q; want nothing", navigate, html)
	}
	// And a panel answering again is opened again, with the tries counted afresh.
	s.on(supervisor.Event{State: supervisor.Starting, PID: 2})
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 2}); !navigate {
		t.Fatal("after the page failed to load, a panel answering is not opened")
	}
}

// A document that came without its styles or its scripts is the white
// window, not the panel.
func TestAPageThatLoadedWithoutItsStylesOrScriptsIsNotThePanel(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.pageSays(pageBroken, testURL)
	c.t = c.t.Add(pageLoadTick)
	if navigate, _ := s.tick(); !navigate {
		t.Fatal("a page that loaded broken is not asked for again")
	}
}

// A page whose document has begun is loading, not lost: asking for it again
// would cut it off and start it over. On the first run of the T-057 update
// stand the new window asked twice before a load finished; neither a fresh
// binary nor a fresh HOME made it happen again, and why it did was not found.
// Such a page gets pageLoadingWait.
func TestAPageThatHasBegunLoadingIsLeftToLoadForPageLoadingWait(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.pageSays(pageLoading, testURL)
	c.t = c.t.Add(pageLoadingWait - pageLoadTick)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick short of pageLoadingWait over a page that has begun = %v, %q; want it left to load", navigate, html)
	}
	c.t = s.askedAt.Add(pageLoadingWait)
	if navigate, _ := s.tick(); !navigate {
		t.Fatal("a page that began and never finished within pageLoadingWait is not asked for again")
	}
	// The next navigation starts from nothing again: it has not begun.
	s.pageSays(pagePanel, testURL)
	if s.asked || !s.showingPanel {
		t.Fatalf("after loading, asked = %v, showing = %v", s.asked, s.showingPanel)
	}
}

func TestThePageLoadScriptSaysWhenTheDocumentBegins(t *testing.T) {
	if script := pageLoadScript(testURL); !strings.Contains(script, `say("`+pageLoading+`")`) {
		t.Fatalf("the script never says %q:\n%s", pageLoading, script)
	}
}

// Once loaded, the time since the navigation no longer matters.
func TestALoadedPageIsNotAskedForAgainByTime(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.pageSays(pagePanel, testURL)
	c.t = c.t.Add(10 * navSilentWait)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick over a loaded page = %v, %q; want nothing", navigate, html)
	}
}

// The page's word is about the panel's page only: the window's own pages
// carry the script too, and must not be taken for the panel.
func TestThePageLoadScriptSpeaksOnlyOnThePanelsOrigin(t *testing.T) {
	script := pageLoadScript(testURL)
	if !strings.Contains(script, `"http://127.0.0.1:7777"`) {
		t.Fatalf("the script does not check the page's origin against the panel's:\n%s", script)
	}
	for _, name := range []string{pageLoadedBindingName, pagePanel, pageBroken, pageLeaving, `link[rel="stylesheet"]`, pageRunningMarker} {
		if !strings.Contains(script, name) {
			t.Errorf("the script never mentions %q", name)
		}
	}
}

// What the script looks for, every page the panel serves must set, from the
// module that page runs: renamed on one side only, or left out of one page,
// and every load of that page reads as broken. Every page, not only the
// panel's own: the window opens "/", which a configured panel answers with the
// start page and an unconfigured one with the setup page -- measured on a
// stand on 2026-09-14, where a check that knew only main.js took the start
// page for broken every time.
func TestEveryPageThePanelServesSetsTheMarkerThePageLoadScriptLooksFor(t *testing.T) {
	pages, err := fs.Glob(web.FS, "*.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 3 {
		t.Fatalf("found pages %v; want at least index, start and setup", pages)
	}
	entry := regexp.MustCompile(`<script type="module" src="/(js/[a-z0-9_-]+\.js)"></script>`)
	for _, page := range pages {
		html, err := fs.ReadFile(web.FS, page)
		if err != nil {
			t.Fatal(err)
		}
		m := entry.FindSubmatch(html)
		if m == nil {
			t.Errorf("%s runs no module the window could hear from", page)
			continue
		}
		src, err := fs.ReadFile(web.FS, string(m[1]))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "dataset."+pageRunningMarker+` = "running"`) {
			t.Errorf("%s never sets dataset.%s: the window will never take %s for loaded", m[1], pageRunningMarker, page)
		}
	}
}

// A page left for another -- the start page choosing a fleet, a reload the
// page asks for -- goes where it was sent, not where the window first opened.
// The window does not know that address until the next page begins, so it
// does not navigate for it: asking for its own URL would take the person back
// to the start page and lose the fleet they chose.
func TestAPageLeftForAnotherIsNotAskedForAtTheWindowsOwnURL(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.pageSays(pagePanel, testURL)
	s.pageSays(pageLeaving, testURL)
	for i := 0; i < 3*pageLoadTries; i++ {
		c.t = c.t.Add(navSilentWait)
		if navigate, html := s.tick(); navigate {
			t.Fatalf("tick %d after the page left navigated to %q; want no navigation", i, s.target())
		} else if html != "" {
			if !strings.Contains(html, "Страница панели не загрузилась") {
				t.Fatalf("tick %d after the page left put up %q", i, html)
			}
			return
		}
	}
	t.Fatal("a page that left and never began another is never said to have failed")
}

// The next page began at the address it was sent to, and stalled: it is asked
// for again at that address.
func TestAPageThatBeganAtAnotherAddressIsAskedForAgainThere(t *testing.T) {
	s, c := newScreen(false)
	fleet := testURL + "?fleet=stand"
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.pageSays(pagePanel, testURL)
	s.pageSays(pageLeaving, testURL)
	s.pageSays(pageLoading, fleet)
	c.t = c.t.Add(pageLoadingWait)
	if navigate, _ := s.tick(); !navigate {
		t.Fatal("a page that began and stalled is not asked for again")
	}
	if got := s.target(); got != fleet {
		t.Fatalf("asked for %q again, want %q", got, fleet)
	}
}

// The failure page says how long the window waited for what it waited on.
func TestThePageFailurePageNamesTheWaitThatRanOut(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	for try := 1; try <= pageLoadTries; try++ {
		s.pageSays(pageLoading, testURL)
		c.t = c.t.Add(pageLoadingWait)
		if _, html := s.tick(); html != "" {
			if !strings.Contains(html, pageLoadingWait.String()) || strings.Contains(html, navSilentWait.String()) {
				t.Fatalf("the failure page after a page that began names the wrong wait:\n%s", html)
			}
			return
		}
	}
	t.Fatal("no failure page after pageLoadTries stalled loads")
}

// What the panel's page said as the window covered it is about a page no
// longer there. On the runner the failure page went up as a navigation had
// just committed; that page then said "loading", "panel" and, replaced,
// "leaving", and the window took the three for its own page: it put the failure
// page up a second time, 0.95 s later, and, had a panel answered in between,
// would have let it go by as already showing.
func TestWhatAPageSaysAfterTheWindowCoveredItIsIgnored(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.cover()
	for _, state := range []string{pageLoading, pagePanel, pageLeaving} {
		s.pageSays(state, testURL)
	}
	if s.showingPanel || s.asked {
		t.Fatalf("after the window covered its page, that page's word left showing = %v, asked = %v", s.showingPanel, s.asked)
	}
	c.t = c.t.Add(10 * navSilentWait)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("tick after a covered page spoke = %v, %q; want nothing", navigate, html)
	}
	if navigate, _ := s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 2}); !navigate {
		t.Fatal("a panel answering after the covered page spoke is not opened")
	}
}

// A page of the window's is named in the log by its heading: on a runner a
// window asked three times for the panel's page and then showed nothing for a
// minute, and whether the failure page had been put up the log could not say.
func TestTheWindowsOwnPagesAreNamedByTheirHeading(t *testing.T) {
	if got := pageHeading(pageFailedPage(testURL, navSilentWait)); got != "Страница панели не загрузилась" {
		t.Fatalf("pageHeading(the failure page) = %q", got)
	}
	if got := pageHeading(blankPage); got != "" {
		t.Fatalf("pageHeading(a page with no heading) = %q, want empty", got)
	}
}
