//go:build darwin

package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/web"
)

// Renamed on one side only, nothing fails: the setup page simply stops finding
// the window's chooser and offers only its text field. This is the check that
// notices. The chooser itself shows a window, so no test here opens it.
func TestTheSetupPageLooksForTheChooserUnderTheNameTheWindowGivesIt(t *testing.T) {
	src, err := fs.ReadFile(web.FS, "js/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "globalThis."+chooseFolderBindingName) {
		t.Fatalf("web/js/setup.js never mentions globalThis.%s: the page will not find the window's folder chooser", chooseFolderBindingName)
	}
}
