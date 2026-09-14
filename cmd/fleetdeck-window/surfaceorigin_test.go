//go:build darwin

package main

import "testing"

func TestASurfaceCallIsTakenOnlyFromThePanelsOwnMainFrame(t *testing.T) {
	const panel = "http://127.0.0.1:7777/"
	cases := []struct {
		name      string
		origin    string
		mainFrame bool
		want      bool
	}{
		{"the panel's page", "http://127.0.0.1:7777", true, true},
		{"a frame the panel's page embeds", "http://127.0.0.1:7777", false, false},
		{"another port on the same host", "http://127.0.0.1:7778", true, false},
		{"another host", "http://localhost:7777", true, false},
		{"another scheme", "https://127.0.0.1:7777", true, false},
		{"no origin WebKit named", "", true, false},
	}
	for _, c := range cases {
		if got := acceptSurfaceMessage(panel, c.origin, c.mainFrame); got != c.want {
			t.Errorf("%s (%q, main frame %v): taken = %v, want %v", c.name, c.origin, c.mainFrame, got, c.want)
		}
	}
	// WebKit reports a scheme's default port as 0, and the origin without it.
	if !acceptSurfaceMessage("http://fleetdeck.local/?fleet=work", "http://fleetdeck.local", true) {
		t.Error("a panel on the default port is not its own origin")
	}
}
