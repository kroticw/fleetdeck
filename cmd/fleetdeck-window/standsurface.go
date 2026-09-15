//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
)

// standReportBindingName is what a page on a stand says of itself through
// (web/js/standreport.js). The board reaches it through its own binding
// (main.go); the side surfaces through the bridge, registered here.
const standReportBindingName = "fleetdeckStandReport"

// handleStandReports gives the side surfaces, on a stand only, the binding
// that puts their report in the window's log: the sessions list's scrollbar
// width, which a screenshot cannot tell apart from a few points more, and the
// grounds the sessions list lies on, which a screenshot of glass cannot tell
// from the glass.
func handleStandReports(b *bridge, onStand bool, logf func(format string, args ...any)) {
	if !onStand {
		return
	}
	b.handle(standReportBindingName, func(surface string, args json.RawMessage) (any, error) {
		logf("fleetdeck-window: the %s surface reports its %s: %s", surface, reportSubject(args), args)
		return nil, nil
	})
}

// reportSubject is what a surface's report is of: its grounds when it says so,
// its scrolling otherwise, as every report was before grounds.
func reportSubject(report json.RawMessage) string {
	var kind struct {
		Report string `json:"report"`
	}
	if json.Unmarshal(report, &kind) == nil && kind.Report == "grounds" {
		return "grounds"
	}
	return "scrolling"
}

// boardStandReportLine is the window's log line for the board's report, the
// JSON the page sent as it sent it: scripts/ci-window-stand.sh reads its fields.
func boardStandReportLine(report json.RawMessage) string {
	return fmt.Sprintf("fleetdeck-window: the board reports its scrolling: %s", report)
}
