//go:build darwin

package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/web"
)

// The window gives the page a function that reloads it with no person
// involved. What that function does is pinned here without a web view:
// it runs on the UI thread, and it navigates to the panel's URL afresh.
// A fresh navigation, not a reload: a reload may take the document from
// cache, a navigation asks the server.

func TestTheReloadBindingNavigatesToThePanelOnTheUIThread(t *testing.T) {
	var queued []func()
	var navigated []string
	dispatch := func(f func()) { queued = append(queued, f) }
	navigate := func(url string) { navigated = append(navigated, url) }

	reload := reloadBinding(dispatch, navigate, "http://127.0.0.1:7777/")
	reload()

	if len(navigated) != 0 {
		t.Fatal("navigated straight from the binding's own goroutine instead of the UI thread")
	}
	if len(queued) != 1 {
		t.Fatalf("queued %d UI-thread calls, want 1", len(queued))
	}
	queued[0]()
	if len(navigated) != 1 || navigated[0] != "http://127.0.0.1:7777/" {
		t.Fatalf("navigated to %v, want the panel's URL once", navigated)
	}
}

// The binding's name is the contract between two languages: Go binds it,
// web/js/buildcheck.js looks for it. Renamed on one side only, nothing
// fails -- the page simply stops finding the window, and goes back to asking
// a person to reload. This is the check that notices.
func TestThePageLooksForTheBindingUnderTheNameTheWindowGivesIt(t *testing.T) {
	src, err := fs.ReadFile(web.FS, "js/buildcheck.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "window."+reloadBindingName) {
		t.Fatalf("web/js/buildcheck.js never mentions window.%s: the page will not find the window's reload", reloadBindingName)
	}
}
