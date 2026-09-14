//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// On a stand the side surfaces say in the window's log what a screenshot cannot
// prove: the sessions list's scrollbar width (web/js/standreport.js). The board
// reports through its own binding (main.go); a surface reaches the window only
// through the bridge.

func TestOnAStandTheSurfacesReportTheirScrollingToTheLog(t *testing.T) {
	var logged []string
	b := newBridge()
	handleStandReports(b, true, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	if !slices.Contains(b.surfaceNames(), standReportBindingName) {
		t.Fatalf("the surfaces' bindings %v lack %s", b.surfaceNames(), standReportBindingName)
	}
	report, _ := json.Marshal(map[string]any{"surface": "sessions", "scrollbarWidth": 6})
	if _, err := b.call("sessions", standReportBindingName, report); err != nil {
		t.Fatal(err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "the sessions surface reports its scrolling") || !strings.Contains(logged[0], "scrollbarWidth") {
		t.Fatalf("logged %q, want the sessions surface's report", logged)
	}
}

func TestOffAStandNoSurfaceReportsItsScrolling(t *testing.T) {
	b := newBridge()
	handleStandReports(b, false, func(string, ...any) { t.Fatal("logged off a stand") })
	if slices.Contains(b.surfaceNames(), standReportBindingName) {
		t.Fatalf("off a stand the surfaces were given %s", standReportBindingName)
	}
}
