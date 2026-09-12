//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

const inBundle = "/a/fleetdeck.app/Contents/MacOS/fleetdeck-window"

// The defect this whole change is about: until now a window with no source
// tree written into it created no update button at all, so a person who
// installed the app from a release had no way to learn that updating existed.
// There is always a button now; what differs is what it does.
func TestAWindowBuiltFromAReleaseCanUpdateFromTheReleasesPage(t *testing.T) {
	way := updateWay(config{tree: "", exe: inBundle, version: "v0.3.0", teamID: "PTLLPQ8LY4"})

	if way.Refusal != "" {
		t.Fatalf("a released app refuses to update: %q", way.Refusal)
	}
	if _, ok := way.Source.(*supervisor.ReleaseSource); !ok {
		t.Fatalf("a released app updates from %T, want a *supervisor.ReleaseSource", way.Source)
	}
}

// The path that already worked keeps working, and keeps working the same way:
// a checkout updates from its checkout, never from a download.
//
// It does so whatever the machine looks like. This test ran green here and red
// on CI, because it used to depend on finding go where FindTools looks, and a
// runner keeps go somewhere else: a build with a checkout written into it then
// called itself a build with no checkout. Which way an app updates follows
// from what the app is, and from nothing else. Tools that have moved are
// TreeSource's refusal to make, at the moment it needs them
// (TestATreeSourceSaysWhichToolItCannotFind).
func TestAWindowBuiltFromACheckoutStillUpdatesFromItsTree(t *testing.T) {
	way := updateWay(config{tree: "/src/fleetdeck", exe: inBundle, version: "dev"})

	if way.Refusal != "" {
		t.Fatalf("a checkout build refuses to update: %q", way.Refusal)
	}
	tree, ok := way.Source.(*supervisor.TreeSource)
	if !ok {
		t.Fatalf("a checkout build updates from %T, want a *supervisor.TreeSource", way.Source)
	}
	if tree.Dir != "/src/fleetdeck" {
		t.Errorf("it would update from %q, not the checkout written into the build", tree.Dir)
	}
}

// A checkout build that is also signed still updates from its checkout: that
// is the build somebody is working on, and downloading a release over it would
// throw their work away.
func TestASignedCheckoutBuildStillPrefersItsTree(t *testing.T) {
	way := updateWay(config{tree: "/src/fleetdeck", exe: inBundle, version: "v0.3.0", teamID: "PTLLPQ8LY4"})

	if _, ok := way.Source.(*supervisor.TreeSource); !ok {
		t.Fatalf("a signed checkout build updates from %T, want a *supervisor.TreeSource", way.Source)
	}
}

// Each refusal is a code rather than a sentence: the page is shown in the
// reader's own language, and an English string from Go cannot be translated
// once it has been built into a message.
func TestAWindowThatCannotUpdateSaysWhichWayItCannot(t *testing.T) {
	cases := []struct {
		name string
		cfg  config
		want string
	}{
		{
			// `go run`, or the binary on its own: there is nothing to replace.
			name: "not in an app bundle",
			cfg:  config{tree: "", exe: "/a/bin/fleetdeck-window", version: "v0.3.0", teamID: "PTLLPQ8LY4"},
			want: refusalNotABundle,
		},
		{
			// `make window-app` on somebody's machine seals the bundle ad hoc.
			// There is no release to match it and no tree written into it.
			name: "built here, signed by nobody",
			cfg:  config{tree: "", exe: inBundle, version: "v0.3.0", teamID: ""},
			want: refusalBuiltHere,
		},
		{
			// A signed bundle that reports no release version cannot be
			// compared with what is published.
			name: "signed but with no release version",
			cfg:  config{tree: "", exe: inBundle, version: "dev", teamID: "PTLLPQ8LY4"},
			want: refusalNoVersion,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			way := updateWay(c.cfg)
			if way.Source != nil {
				t.Fatalf("it offered to update from %T", way.Source)
			}
			if way.Refusal != c.want {
				t.Fatalf("refusal %q, want %q", way.Refusal, c.want)
			}
		})
	}
}

// A window that cannot update is still a window that says so. The page is told
// once, at startup, rather than left to guess from a button that is not there.
func TestTheRefusalIsToldToThePageAtStartup(t *testing.T) {
	p := refusalProgress(refusalBuiltHere)

	if p.Step != "cannot" {
		t.Fatalf("step %q, want cannot", p.Step)
	}
	if p.Reason != refusalBuiltHere {
		t.Fatalf("reason %q, want %q", p.Reason, refusalBuiltHere)
	}
}

// Every refusal a person can meet has a code the page can translate. A bare
// error reaching the page as English text is the failure this guards against.
func TestEveryRefusalTheUpdateCanMeetHasACode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no network", &supervisor.ReleasesUnreachableError{URL: "https://github.com", Err: errors.New("no route")}, reasonOffline},
		{"no releases published", &supervisor.NoReleasesError{URL: "https://github.com/x/y"}, reasonNoReleases},
		{"no room on the disk", &supervisor.NoRoomError{Dir: "/Applications", Need: 1 << 30, Free: 1 << 20}, reasonNoRoom},
		{"already running", supervisor.ErrBusy, reasonBusy},
		{"changed after signing", &supervisor.SealError{Reason: supervisor.SealBroken}, "seal:broken"},
		{"signed by nobody", &supervisor.SealError{Reason: supervisor.SealNotDeveloperID}, "seal:not-developer-id"},
		{"signed by somebody else", &supervisor.SealError{Reason: supervisor.SealWrongTeam, Want: "A", Got: "B"}, "seal:wrong-team"},
		{"not notarized", &supervisor.SealError{Reason: supervisor.SealNotNotarized}, "seal:not-notarized"},
		{"anything else", errors.New("the source tree has uncommitted edits"), reasonOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := resultProgress(c.err)
			if p.Reason != c.want {
				t.Fatalf("reason %q, want %q", p.Reason, c.want)
			}
			if p.Step != "failed" && p.Step != "busy" {
				t.Fatalf("step %q, want failed or busy", p.Step)
			}
		})
	}
}

// A refusal a person can act on has to carry what it is about: which team
// signed the thing, how much room is missing. The code names the kind of
// refusal; the detail names this one.
func TestARefusalCarriesWhatItIsAbout(t *testing.T) {
	p := resultProgress(&supervisor.SealError{Reason: supervisor.SealWrongTeam, Want: "PTLLPQ8LY4", Got: "ZZZZZZZZZZ"})
	if !strings.Contains(p.Detail, "ZZZZZZZZZZ") {
		t.Errorf("a wrong-team refusal does not name the team: %q", p.Detail)
	}

	p = resultProgress(&supervisor.NoRoomError{Dir: "/Applications", Need: 2 << 30, Free: 1 << 30})
	if !strings.Contains(p.Detail, "/Applications") {
		t.Errorf("a no-room refusal does not say where: %q", p.Detail)
	}
}

// The page reads one JSON argument, and the reason has to be in it or the
// page cannot translate anything.
func TestTheReasonReachesThePageBesideTheStep(t *testing.T) {
	script := progressScript(supervisor.Progress{Step: "failed", Detail: "x"}, reasonOffline)
	open := strings.Index(script, "(")
	var got map[string]string
	if err := json.Unmarshal([]byte(script[open+1:len(script)-1]), &got); err != nil {
		t.Fatalf("the argument is not JSON: %v (%s)", err, script)
	}
	if got["reason"] != reasonOffline {
		t.Fatalf("the page would get reason %q, want %q", got["reason"], reasonOffline)
	}
}
