//go:build darwin

package main

import (
	"fmt"
	"strings"

	"github.com/kroticw/fleetdeck/web"
)

// topBandBindingName is how every page in the board's web view tells the window
// how far down from its top nothing is (web/js/topband.js): the band the window
// is dragged by.
const topBandBindingName = "fleetdeckTopBand"

// topBandScript is web/js/topband.js as a user script for every page the
// board's web view shows -- the board, the start page, the window's own pages
// -- telling the window through topBandBindingName. A user script is not a
// module, so the file's exports are taken off; it is the same file the page's
// tests run.
func topBandScript() string {
	src, err := web.FS.ReadFile("js/topband.js")
	if err != nil {
		// Embedded at build time: a build without it is broken, not a page.
		panic(fmt.Sprintf("web/js/topband.js is not embedded: %v", err))
	}
	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "export ")
	}
	return "(() => {\n" + strings.Join(lines, "\n") +
		fmt.Sprintf("\nwatchTopBand(window, (height) => { if (typeof window.%[1]s === \"function\") window.%[1]s(height); });\n})();\n", topBandBindingName)
}
