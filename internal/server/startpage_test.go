package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// What "/" serves changed with the start page, and the change is deliberate
// enough to be pinned from both sides.
//
// Before: "/" was the panel, showing the first fleet, and an address naming no
// fleet meant the first one everywhere (docs/engineering/multiple-fleets.md §3).
// Now: "/" is the start page, and the panel is "/?fleet=<name>". The fleet still
// lives in the address and nowhere else; what changed is only what an address
// with no fleet at all opens.
//
// "/?fleet=" — the parameter present and empty — stays the panel on the first
// fleet. That is what withFleet builds for a panel that has never named a fleet
// and what fleet.Select already reads as "the first one", so the old meaning is
// still reachable, spelled out rather than implied.

func body(t *testing.T, target string) string {
	t.Helper()
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 at %s, got %d", target, rec.Code)
	}
	return rec.Body.String()
}

func TestRootWithNoFleetServesTheStartPage(t *testing.T) {
	got := body(t, "/")
	if !strings.Contains(got, `id="start"`) {
		t.Fatal(`"/" must serve the start page`)
	}
	if strings.Contains(got, `id="board"`) {
		t.Fatal(`"/" must not serve the panel: the panel is "/?fleet=<name>"`)
	}
}

func TestRootWithAFleetServesThePanel(t *testing.T) {
	got := body(t, "/?fleet=vpn")
	if !strings.Contains(got, `id="board"`) {
		t.Fatal(`"/?fleet=vpn" must serve the panel`)
	}
}

// An empty fleet parameter is the first fleet, not "no fleet named": it is what
// the wizard and the header build for a panel that has only ever had one fleet.
func TestRootWithAnEmptyFleetParameterStillServesThePanel(t *testing.T) {
	got := body(t, "/?fleet=")
	if !strings.Contains(got, `id="board"`) {
		t.Fatal(`"/?fleet=" must serve the panel on the first fleet`)
	}
}

// The start page is a page of the same interface, so it carries the same policy
// and the same validator as everything else the binary serves. Without this the
// one page a person lands on first would be the one page with no CSP.
func TestStartPageCarriesTheSameContentSecurityPolicy(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	want := "default-src 'self'; connect-src 'self' ws: wss:; script-src 'self'; style-src 'self' 'unsafe-inline'"
	if got := rec.Header().Get("Content-Security-Policy"); got != want {
		t.Fatalf("want Content-Security-Policy %q, got %q", want, got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("want Cache-Control no-cache, got %q", got)
	}
}

// The page is a file of its own as well as what "/" serves, so a reference to
// it from anywhere resolves.
func TestStartPageIsAlsoServedByItsOwnPath(t *testing.T) {
	if got := body(t, "/start.html"); !strings.Contains(got, `id="start"`) {
		t.Fatal("/start.html must serve the start page")
	}
}

// "/index.html" is not "/", so it goes to the file server, which redirects
// ".../index.html" to "./" — the start page now, where it used to be the
// panel. The query survives that redirect, so the address that names a fleet
// still lands on the panel. Pinned because it is the one other spelling of the
// root a person may have bookmarked, and the cost differs: with a fleet
// nothing changes, without one it is a click.
func TestIndexHTMLFollowsTheRootItRedirectsTo(t *testing.T) {
	d, _ := testDeps()
	for target, want := range map[string]string{
		"/index.html":           `id="start"`,
		"/index.html?fleet=vpn": `id="board"`,
	} {
		rec := httptest.NewRecorder()
		New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code == http.StatusMovedPermanently {
			// The file server redirects to a relative "./", which has to be
			// resolved against the address that was asked for before it can be
			// requested again.
			base, err := url.Parse(target)
			if err != nil {
				t.Fatal(err)
			}
			loc, err := url.Parse(rec.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			rec2 := httptest.NewRecorder()
			New(d).ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, base.ResolveReference(loc).String(), nil))
			rec = rec2
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", target, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: want %s in the body", target, want)
		}
	}
}

// The start page holds no terminal open, so it must not pull xterm.js in. This
// is not tidiness: a terminal on this page would attach to a session of a fleet
// nobody is looking at, and an attach sets that session's size for everyone
// attached to it (docs/engineering/live-terminal.md).
func TestStartPageLoadsNoTerminal(t *testing.T) {
	// The tag, not the word: the page's own comment says why it loads no
	// terminal, and a test that matched that comment would fail on the
	// explanation rather than on a script.
	if strings.Contains(body(t, "/"), `src="/vendor/`) {
		t.Fatal("the start page must not load the vendored terminal: it opens no session")
	}
}
