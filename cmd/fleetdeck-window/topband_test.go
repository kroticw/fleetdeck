//go:build darwin

package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestTheTopBandScriptIsThePagesFileAsAPlainScriptTellingTheWindow(t *testing.T) {
	script := topBandScript()
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "export ") {
			t.Fatalf("a user script is not a module, and still has %q", line)
		}
	}
	for _, want := range []string{"function freeTopHeight(", "function watchTopBand(", "window." + topBandBindingName + "(height)"} {
		if !strings.Contains(script, want) {
			t.Errorf("the top band script has no %q", want)
		}
	}
}

// The page's tallest band and the window's are one number, written twice.
func TestThePagesTopBandIsTheBoardsTopInset(t *testing.T) {
	want := "const TOP_BAND_MAX = " + strconv.FormatFloat(boardInsetTop, 'f', -1, 64) + ";"
	if !strings.Contains(topBandScript(), want) {
		t.Fatalf("web/js/topband.js has no %q", want)
	}
}

func TestTheBandIsWhatThePageSaysUpToTheBoardsTopInset(t *testing.T) {
	c := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348}, glassModeGlass)
	for _, tc := range []struct{ said, want float64 }{{40, 40}, {0, 0}, {120, boardInsetTop}, {-3, 0}} {
		got := c.topBand(tc.said)
		if len(got) != 1 || got[0] != (setDragBand{Height: tc.want}) {
			t.Fatalf("page says %v: effects %#v, want one setDragBand{%v}", tc.said, got, tc.want)
		}
	}
}

// In full screen a window is not moved, and a double click on its top must not
// zoom or minimize a window the system is taking out of full screen.
func TestThereIsNoBandInFullScreen(t *testing.T) {
	c := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348}, glassModeGlass)
	c.resized(1512, 982, false)
	c.topBand(40)
	if !hasEffect(c.resized(1512, 982, true), setDragBand{Height: 0}) {
		t.Fatal("going full screen did not take the band away")
	}
	if got := c.topBand(50); got[0] != (setDragBand{Height: 0}) {
		t.Fatalf("the page's word in full screen gave %#v, want no band", got)
	}
	if hasEffectOfType[setDragBand](c.resized(1600, 1000, true)) {
		t.Fatal("a resize within full screen touched the band")
	}
	if !hasEffect(c.resized(1512, 982, false), setDragBand{Height: 50}) {
		t.Fatal("leaving full screen did not give back the band the page last said")
	}
	if hasEffectOfType[setDragBand](c.resized(1400, 900, false)) {
		t.Fatal("a plain resize touched the band")
	}
}

func hasEffect(effects []effect, want effect) bool {
	for _, e := range effects {
		if e == want {
			return true
		}
	}
	return false
}

func hasEffectOfType[T effect](effects []effect) bool {
	for _, e := range effects {
		if _, ok := e.(T); ok {
			return true
		}
	}
	return false
}
