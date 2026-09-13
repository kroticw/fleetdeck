package state

import (
	"io/fs"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/web"
)

// The unanswered verdict is computed twice: here, for the banners, and in
// web/js/needs.js, for the screen. Two copies of a threshold drift apart silently --
// each side looks right on its own, and the panel ends up badging one session the
// notifier treats differently. This reads the browser's constant out of the embedded
// file itself, so changing one number without the other fails here.
//
// The file is named _browser_test rather than _js_test on purpose: a _js suffix is a
// GOOS build constraint, and Go would silently leave the file out on every other system.
func TestUnansweredLimitMatchesTheBrowser(t *testing.T) {
	src, err := fs.ReadFile(web.FS, "js/needs.js")
	if err != nil {
		t.Fatalf("read embedded needs.js: %v", err)
	}
	m := regexp.MustCompile(`export const UNANSWERED_LIMIT_MS = (\d+) \* 60 \* 1000;`).FindSubmatch(src)
	if m == nil {
		t.Fatal("needs.js must declare `export const UNANSWERED_LIMIT_MS = <minutes> * 60 * 1000;`")
	}
	mins, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(mins) * time.Minute; got != UnansweredLimit {
		t.Fatalf("needs.js says %s, state.UnansweredLimit says %s", got, UnansweredLimit)
	}
}
