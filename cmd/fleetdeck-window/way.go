//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// ownTeamID is the Apple team this app is signed by, or "" when it is not
// signed with a Developer ID -- a build somebody made on their own machine.
//
// Read from the running app rather than written into the code on purpose: a
// fork with its own certificate then updates itself from its own releases,
// and nothing signed by anybody else can replace this one.
func ownTeamID(exe string) string {
	bundle := bundleOf(exe)
	if bundle == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	team, err := supervisor.TeamIDOf(ctx, codesignPath, bundle)
	if err != nil {
		return ""
	}
	return team
}

// Where a released app looks for a newer one. A constant rather than a
// setting: an app that could be pointed at another address by anything but a
// new build of itself would be a way to hand somebody a different program.
const releasesBase = "https://github.com/kroticw/fleetdeck"

// The system's own signing tools, by absolute path. Never looked up on PATH:
// these decide whether this app replaces itself, and both live in macOS
// itself -- codesign in /usr/bin, spctl in /usr/sbin -- so there is nothing to
// search for. /usr/bin/ditto unpacks the archive the way Archive Utility does.
const (
	codesignPath = "/usr/bin/codesign"
	spctlPath    = "/usr/sbin/spctl"
	dittoPath    = "/usr/bin/ditto"
)

// Why this copy of the app cannot update itself. Codes, not sentences: the
// page is read in the reader's own language, and a sentence built in Go
// arrives already in English.
const (
	// refusalNotABundle: running as a bare binary -- `go run`, or the
	// executable on its own. There is no app to replace.
	refusalNotABundle = "not-a-bundle"
	// refusalBuiltHere: an app bundle somebody built on this machine. It has
	// no source tree written into it and no Developer ID on it, so there is
	// nothing to bring forward and no release it corresponds to.
	refusalBuiltHere = "built-here"
	// refusalNoVersion: signed like a release but reporting no release
	// version, so there is nothing to compare with what is published.
	refusalNoVersion = "no-version"
)

// Why an update that was tried did not happen.
const (
	reasonOffline    = "offline"
	reasonNoReleases = "no-releases"
	reasonNoRoom     = "no-room"
	reasonBusy       = "busy"
	reasonOther      = "other"
)

// config is what the window knows about itself when it works out how it can
// update. Gathered once, at startup, and passed in rather than read inside, so
// that every case can be put to the function in a test.
type config struct {
	// tree is the source tree `make window-app` wrote into this build, empty
	// for every other build.
	tree string
	// exe is this binary's path; the bundle is worked out from it.
	exe string
	// version is what this build reports as its release version, "dev" for a
	// build from a checkout.
	version string
	// teamID is the Apple team this bundle is signed by, empty when it is not
	// signed with a Developer ID.
	teamID string
}

// way is how this copy of the app updates itself, or why it cannot.
type way struct {
	// Source is where a new app would come from; nil when there is none.
	Source supervisor.Source
	// Refusal is the code for why there is none.
	Refusal string
}

// updateWay works out which of the two ways this app updates itself.
//
// The order matters. A build from a checkout updates from its checkout even
// when it is also signed, because that is the build somebody is working on and
// downloading over it would throw their work away. Everything else that is a
// real installed app updates from the releases page.
func updateWay(cfg config) way {
	bundle := bundleOf(cfg.exe)
	if bundle == "" {
		return way{Refusal: refusalNotABundle}
	}
	if cfg.tree != "" {
		tools, err := supervisor.FindTools(supervisor.Tools{Git: gitPath, Go: goPath, Make: makePath}, supervisor.FileExists)
		if err != nil {
			// The tree is written in but the tools that built it are gone.
			// That is a checkout build with a broken machine, not a release.
			return way{Refusal: refusalBuiltHere}
		}
		return way{Source: &supervisor.TreeSource{
			Tree: &supervisor.Tree{
				Dir: cfg.tree, Remote: updateRemote, Branch: updateBranch,
				Git: tools.Git, Env: supervisor.BuildEnv(tools, os.Environ()),
			},
			Tools:   tools,
			Env:     os.Environ(),
			Running: ownRevision(),
		}}
	}
	if cfg.teamID == "" {
		return way{Refusal: refusalBuiltHere}
	}
	if _, err := supervisor.Newer(cfg.version, cfg.version); err != nil {
		return way{Refusal: refusalNoVersion}
	}
	return way{Source: &supervisor.ReleaseSource{
		Releases: &supervisor.Releases{Base: releasesBase},
		Seal:     &supervisor.Seal{Codesign: codesignPath, Spctl: spctlPath, TeamID: cfg.teamID},
		Running:  cfg.version,
		Ditto:    dittoPath,
	}}
}

// report is one thing the page is told about updating: what is happening, why
// it stopped if it did, and the particulars.
type report struct {
	Step   string `json:"step"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// refusalProgress is what the page is told at startup by a window that cannot
// update itself at all. It is told rather than left to work it out from a
// button that is not there: the silence was the defect.
func refusalProgress(code string) report {
	return report{Step: "cannot", Reason: code}
}

// resultProgress turns an update's error into something the page can say in
// the reader's own language: a code it can translate, and the particulars of
// this one refusal beside it.
func resultProgress(err error) report {
	if errors.Is(err, supervisor.ErrBusy) {
		return report{Step: "busy", Reason: reasonBusy}
	}
	return report{Step: "failed", Reason: reasonOf(err), Detail: err.Error()}
}

func reasonOf(err error) string {
	var (
		unreachable *supervisor.ReleasesUnreachableError
		noReleases  *supervisor.NoReleasesError
		noRoom      *supervisor.NoRoomError
		seal        *supervisor.SealError
	)
	switch {
	case errors.As(err, &unreachable):
		return reasonOffline
	case errors.As(err, &noReleases):
		return reasonNoReleases
	case errors.As(err, &noRoom):
		return reasonNoRoom
	case errors.As(err, &seal):
		// The seal's own reasons are already codes; they keep their names
		// under a prefix so the page can tell a refusal about the signature
		// from every other kind.
		return "seal:" + string(seal.Reason)
	default:
		return reasonOther
	}
}
