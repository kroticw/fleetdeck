//go:build darwin

package main

import "encoding/json"

// standReportBindingName is what a page on a stand says of itself through
// (web/js/standreport.js). The board reaches it through its own binding
// (main.go); the side surfaces through the bridge, registered here.
const standReportBindingName = "fleetdeckStandReport"

// handleStandReports gives the side surfaces, on a stand only, the binding
// that puts their report in the window's log: the sessions list's scrollbar
// width, which a screenshot cannot tell apart from a few points more.
func handleStandReports(b *bridge, onStand bool, logf func(format string, args ...any)) {
	if !onStand {
		return
	}
	b.handle(standReportBindingName, func(surface string, args json.RawMessage) (any, error) {
		logf("fleetdeck-window: the %s surface reports its scrolling: %s", surface, args)
		return nil, nil
	})
}
