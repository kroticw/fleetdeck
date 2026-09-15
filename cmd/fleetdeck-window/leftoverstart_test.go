//go:build darwin

package main

import "testing"

// Only a window started as the installed app removes what an earlier update
// left in the staging directory: not a window started by an update, and not a
// window started from the staging directory itself.
func TestOnlyTheInstalledAppsStartRemovesALeftoverBundle(t *testing.T) {
	const (
		installed = "/Applications/fleetdeck.app/Contents/MacOS/fleetdeck-window"
		staged    = "/Applications/.fleetdeck-update/fleetdeck.app/Contents/MacOS/fleetdeck-window"
	)
	for _, c := range []struct {
		name      string
		handover  string
		exe, told string
		want      bool
	}{
		{name: "the installed app", exe: installed, want: true},
		{name: "a window from the staging directory", exe: staged},
		{name: "a window started by an update", handover: "/Applications/.fleetdeck-update/handover", exe: staged, told: "/Applications/fleetdeck.app"},
		{name: "a binary outside any bundle", exe: "/usr/local/bin/fleetdeck-window"},
	} {
		if got := retiresLeftover(c.handover, canonicalBundle(c.exe, c.told), false); got != c.want {
			t.Errorf("%s: removes a leftover bundle = %v, want %v", c.name, got, c.want)
		}
	}
}
