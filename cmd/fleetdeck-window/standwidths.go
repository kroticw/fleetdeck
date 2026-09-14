//go:build darwin

package main

import "github.com/kroticw/fleetdeck/internal/supervisor"

// standWidthsSuite is the defaults suite a window on a stand keeps its panels'
// widths and folds in. Not the stand bundle's own identifier: a suite may not
// be named after the main bundle, which on a stand is that bundle.
const standWidthsSuite = supervisor.StandBundleID + ".widths"

// widthsSuite is where the panels' widths are kept: "" -- the app's own
// defaults -- for the app, and the stands' own suite for a window on a stand.
// cfprefsd keeps defaults per user, not per HOME, so a stand's HOME of its own
// would still write the widths the operator's app opens with.
func widthsSuite(standSocket string) string {
	if standSocket == "" {
		return ""
	}
	return standWidthsSuite
}
