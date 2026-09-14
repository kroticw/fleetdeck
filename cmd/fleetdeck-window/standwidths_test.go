//go:build darwin

package main

import (
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

func TestAStandKeepsItsPanelWidthsAwayFromTheApps(t *testing.T) {
	if got := widthsSuite(""); got != "" {
		t.Fatalf("the app's widths go to suite %q, want its own defaults", got)
	}
	got := widthsSuite("/tmp/stand/no-daemon-here.sock")
	if got == "" {
		t.Fatal("a window on a stand keeps its widths in the app's own defaults, which the operator's app reads")
	}
	if got == supervisor.StandBundleID || !strings.HasPrefix(got, supervisor.StandBundleID) {
		t.Fatalf("a stand's widths go to suite %q, want one of the stands' own, not named after the stand bundle", got)
	}
}
