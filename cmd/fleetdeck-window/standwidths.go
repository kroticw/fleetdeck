//go:build darwin

package main

import "github.com/kroticw/fleetdeck/internal/supervisor"

// standWidthsSuite is the defaults suite a window on a stand keeps its panels'
// widths and folds in. Not the stand bundle's own identifier: a suite may not
// be named after the main bundle, which on a stand is that bundle.
const standWidthsSuite = supervisor.StandBundleID + ".widths"

// devWidthsSuite is the suite a dev app keeps its panels' widths in.
const devWidthsSuite = supervisor.DevBundleID + ".widths"

// widthsSuite is where the panels' widths are kept: "" -- the app's own
// defaults -- for the app, the stands' own suite for a window on a stand, and
// the dev apps' own for a dev app. cfprefsd keeps defaults per user, not per
// HOME, so a stand's HOME of its own would still write the widths the
// operator's app opens with.
func widthsSuite(standSocket string, dev bool) string {
	if dev {
		return devWidthsSuite
	}
	if standSocket == "" {
		return ""
	}
	return standWidthsSuite
}
