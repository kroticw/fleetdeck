//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// A side surface's web view is named for its kind and its generation, and the
// name comes back with every word from it: an event from a web view taken down
// is told apart from one from the web view shown now.
func TestASurfacesNameCarriesItsKindAndGeneration(t *testing.T) {
	name := surfaceName("orchestrator", 7)
	if name != "orchestrator@7" {
		t.Fatalf("name = %q", name)
	}
	if kind, gen, ok := parseSurfaceName(name); !ok || kind != "orchestrator" || gen != 7 {
		t.Fatalf("parse(%q) = %q, %d, %v", name, kind, gen, ok)
	}
	if got := surfaceKind("sessions@3"); got != "sessions" {
		t.Fatalf("surfaceKind = %q", got)
	}
	for _, bad := range []string{"", "orchestrator", "orchestrator@", "@7", "orchestrator@x", "orchestrator@-1", "board@1", "sessions@1@2"} {
		if kind, gen, ok := parseSurfaceName(bad); ok {
			t.Fatalf("parse(%q) = %q, %d, true: not a side surface's name", bad, kind, gen)
		}
	}
}

// createdGeneration is the generation of the surfaces effects create.
func createdGeneration(t *testing.T, effects []effect) int {
	t.Helper()
	for _, e := range effects {
		if cs, ok := e.(createSurfaces); ok {
			return cs.Gen
		}
	}
	t.Fatalf("no surfaces are created: %#v", effects)
	return 0
}

// Every pair of surfaces made has a generation of its own; the same page loaded
// again keeps the surfaces it has.
func TestEachPairOfSurfacesMadeHasAGenerationOfItsOwn(t *testing.T) {
	c := started()
	first := createdGeneration(t, c.layout(1, "panel", "work"))
	for _, e := range c.layout(1, "panel", "work") {
		if _, ok := e.(createSurfaces); ok {
			t.Fatal("the same page loaded again made its surfaces again")
		}
	}
	second := createdGeneration(t, c.layout(1, "panel", "other"))
	c.layout(1, "start", "")
	third := createdGeneration(t, c.layout(1, "panel", "other"))
	if first >= second || second >= third {
		t.Fatalf("generations %d, %d, %d: each pair must have a later one", first, second, third)
	}
}

// switched is the fleet switch of 15.09: fleet work framed and both its surfaces
// shown, then fleet other framed in its place. old is work's surfaces'
// generation, cur other's.
func switched(t *testing.T) (c *controller, old, cur int) {
	t.Helper()
	c = started()
	old = createdGeneration(t, c.layout(1, "panel", "work"))
	for _, kind := range sideSurfaces {
		c.surfaceNavigatedFrom(surfaceName(kind, old), navEvent{kind: navCommitted, href: "http://127.0.0.1:7777/?fleet=work"})
		c.pageLoadedFrom(surfaceName(kind, old), pagePanel)
	}
	cur = createdGeneration(t, c.layout(1, "panel", "other"))
	return c, old, cur
}

// watches is a copy of every side surface's page watch now.
func watches(c *controller) map[string]pageWatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]pageWatch{}
	for kind, w := range c.loads {
		out[kind] = *w
	}
	return out
}

// After a fleet switch the surfaces taken down can still be heard from: their
// web views live until the effects that take them down are carried out, and
// their pages' calls are answered off the main thread. No word from them —
// WebKit's about a navigation, the page's about its load, a navigation it asks
// to make — changes what the window knows of the surfaces shown now, or makes
// it do anything. Asked often enough to give up on a page, had it counted.
func TestNoWordFromAnEarlierGenerationChangesTheSurfacesShownNow(t *testing.T) {
	c, old, _ := switched(t)
	before := watches(c)
	work := "http://127.0.0.1:7777/?fleet=work"
	events := []navEvent{
		{kind: navStarted, href: work},
		{kind: navCommitted, href: work},
		{kind: navFinished, href: work},
		{kind: navFailedProvisional, href: work, errDomain: "NSURLErrorDomain", errCode: -1004},
		{kind: navFailed, href: work, errDomain: "NSURLErrorDomain", errCode: -1004},
		{kind: navProcessGone},
	}
	for _, kind := range sideSurfaces {
		name := surfaceName(kind, old)
		for range pageLoadTries + 1 {
			for _, e := range events {
				if got := c.surfaceNavigatedFrom(name, e); len(got) != 0 {
					t.Fatalf("%s: WebKit's %s from the surface taken down did %#v", name, e.kind, got)
				}
			}
			for _, state := range []string{"loading", pagePanel, "leaving", "broken"} {
				if got := c.pageLoadedFrom(name, state); len(got) != 0 {
					t.Fatalf("%s: its page saying %q did %#v", name, state, got)
				}
			}
			for _, target := range []string{"https://github.com/kroticw/fleetdeck", "http://127.0.0.1:7777/setup.html", "http://127.0.0.1:7777/?fleet=other"} {
				if allow, got := c.navigateFrom(name, target, true); allow || len(got) != 0 {
					t.Fatalf("%s: its navigation to %s was allowed=%v and did %#v", name, target, allow, got)
				}
			}
		}
	}
	if got := watches(c); !reflect.DeepEqual(got, before) {
		t.Fatalf("the surfaces shown now were changed by the ones taken down:\nbefore %+v\nafter  %+v", before, got)
	}
}

// The web content process of a surface taken down going away is the old web
// view's, not the one shown now: nothing is reloaded and no loss is counted.
func TestAnEarlierGenerationsLostProcessDoesNotReloadTheSurfaceShownNow(t *testing.T) {
	c, old, cur := switched(t)
	for _, kind := range sideSurfaces {
		c.surfaceNavigatedFrom(surfaceName(kind, cur), navEvent{kind: navCommitted, href: "http://127.0.0.1:7777/?fleet=other"})
		c.pageLoadedFrom(surfaceName(kind, cur), pagePanel)
	}
	if got := c.surfaceNavigatedFrom(surfaceName("sessions", old), navEvent{kind: navProcessGone}); len(got) != 0 {
		t.Fatalf("the old web view's process going away did %#v", got)
	}
	if w := watches(c)["sessions"]; w.processLosses != 0 || !w.showingPanel {
		t.Fatalf("the surface shown now after the old one's process went: %+v", w)
	}
	// The control: the surface shown now losing its own process is reloaded.
	if got, want := c.surfaceNavigatedFrom(surfaceName("sessions", cur), navEvent{kind: navProcessGone}), []effect{reloadSurface{Surface: "sessions"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the surface shown now losing its process did %#v, want %#v", got, want)
	}
}

// Words from the surfaces shown now are taken as they always were.
func TestWordsFromTheCurrentGenerationAreTakenAsBefore(t *testing.T) {
	c, _, cur := switched(t)
	c.surfaceNavigatedFrom(surfaceName("sessions", cur), navEvent{kind: navFailedProvisional, href: "http://127.0.0.1:7777/?fleet=other", errDomain: "NSURLErrorDomain", errCode: -1004})
	if w := watches(c)["sessions"]; !w.retrySoon {
		t.Fatalf("a failure of the surface shown now was not taken: %+v", w)
	}
	if got := c.pageLoadedFrom(surfaceName("orchestrator", cur), pagePanel); len(got) == 0 {
		t.Fatal("the page shown now saying it loaded was not taken")
	}
	if allow, _ := c.navigateFrom(surfaceName("orchestrator", cur), "http://127.0.0.1:7777/?fleet=other", true); !allow {
		t.Fatal("the surface shown now may not load its own page")
	}
}

// A word under a name that is no side surface's changes nothing either.
func TestAWordUnderNoSurfacesNameChangesNothing(t *testing.T) {
	c, _, _ := switched(t)
	before := watches(c)
	for _, name := range []string{"sessions", "board", "sessions@", "orchestrator@x"} {
		if got := c.surfaceNavigatedFrom(name, navEvent{kind: navProcessGone}); len(got) != 0 {
			t.Fatalf("%q: %#v", name, got)
		}
		if got := c.pageLoadedFrom(name, pagePanel); len(got) != 0 {
			t.Fatalf("%q: %#v", name, got)
		}
		if allow, got := c.navigateFrom(name, "https://github.com/kroticw/fleetdeck", true); allow || len(got) != 0 {
			t.Fatalf("%q: allowed=%v, %#v", name, allow, got)
		}
	}
	if got := watches(c); !reflect.DeepEqual(got, before) {
		t.Fatalf("watches changed: %+v", got)
	}
}

// A page's call is queued for the surface of its own generation only: a call
// from a surface taken down never lands in the queue of the one shown now.
func TestASurfacesCallGoesOnlyToTheSurfaceOfItsGeneration(t *testing.T) {
	shown := &surface{kind: "sessions", gen: 2}
	surfaces := map[string]*surface{"sessions": shown}
	if got := surfaceNamed(surfaces, "sessions@2"); got != shown {
		t.Fatalf("the surface's own call went to %v", got)
	}
	for _, name := range []string{"sessions@1", "sessions@3", "orchestrator@2", "sessions"} {
		if got := surfaceNamed(surfaces, name); got != nil {
			t.Fatalf("a call under %q went to %v", name, got)
		}
	}
}

// A surface's name reaches the bindings with its generation; what the window's
// log says of the surface is still its kind, which scripts/ci-window-stand.sh
// reads.
func TestAStandReportUnderASurfacesNameSaysItsKind(t *testing.T) {
	b := newBridge()
	var logged []string
	handleStandReports(b, true, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	if _, err := b.call("sessions@2", standReportBindingName, json.RawMessage(`{"report":"grounds"}`)); err != nil {
		t.Fatal(err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "the sessions surface reports its grounds") {
		t.Fatalf("logged %q", logged)
	}
}
