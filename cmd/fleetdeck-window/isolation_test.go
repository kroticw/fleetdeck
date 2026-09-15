//go:build darwin

package main

import (
	"strings"
	"testing"
)

// A panel finds the fleet daemon by uid, not by HOME, and -stand-socket is
// the only thing that keeps it off the real one. The window starts its panels
// with arguments of its own, so on 2026-09-14 a stand's window started panels
// that could reach the operator's fleet. A window given a stand's socket in
// its environment hands it to every panel it starts.

func env(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestAWindowOutsideAStandStartsItsPanelsAsBefore(t *testing.T) {
	socket, err := standIsolation(env(nil))
	if err != nil || socket != "" {
		t.Fatalf("standIsolation with nothing set = %q, %v; want none", socket, err)
	}
	if got := panelArgs(4242, socket, 0, ""); strings.Join(got, " ") != "--owner-pid 4242" {
		t.Fatalf("panelArgs = %q", got)
	}
}

func TestAWindowOnAStandHandsTheStandSocketToItsPanels(t *testing.T) {
	socket, err := standIsolation(env(map[string]string{standSocketEnv: "/tmp/stand/no-daemon.sock"}))
	if err != nil || socket != "/tmp/stand/no-daemon.sock" {
		t.Fatalf("standIsolation = %q, %v", socket, err)
	}
	got := strings.Join(panelArgs(4242, socket, 0, ""), " ")
	if want := "--owner-pid 4242 --stand-socket /tmp/stand/no-daemon.sock"; got != want {
		t.Fatalf("panelArgs on a stand = %q, want %q", got, want)
	}
}

// Set, but to nothing -- a script templating an unset variable -- is a stand
// that cannot say where its socket is. The window refuses rather than start a
// panel that would find the real daemon, as the panel refuses an empty
// -stand-socket (cmd/fleetdeck, checkStandSocket).
func TestAWindowOnAStandWithNoSocketRefuses(t *testing.T) {
	for _, value := range []string{"", "   "} {
		if _, err := standIsolation(env(map[string]string{standSocketEnv: value})); err == nil || !strings.Contains(err.Error(), standSocketEnv) {
			t.Fatalf("standIsolation(%q) = %v; want a refusal naming %s", value, err, standSocketEnv)
		}
	}
}
