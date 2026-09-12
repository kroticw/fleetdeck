package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// sealFacts is what `codesign --display --verbose=4` says about one piece of
// code: who signed it, and under what.
//
// Every field is read from a line of codesign's own output, and every one of
// them was measured rather than taken from documentation -- the strings the
// tests compare against are that measurement. What this type deliberately does
// not carry is a verdict: these facts are true of a bundle whose contents have
// been changed since it was signed, because --display reads the signature and
// does not check it. Measured on 2026-09-12: a bundle with one byte appended
// to its icon still displays `Authority=Developer ID Application: ...`,
// `TeamIdentifier=PTLLPQ8LY4` and `Notarization Ticket=stapled`. So these
// facts mean nothing until --verify has passed, and Seal.Verify runs them in
// that order.
type sealFacts struct {
	// DeveloperID: the leaf authority is a Developer ID Application
	// certificate.
	DeveloperID bool
	// ChainsToApple: the certificate chains through Apple's Developer ID
	// authority to the Apple root.
	ChainsToApple bool
	// TeamID is the team the certificate belongs to, empty when there is none.
	TeamID string
	// HardenedRuntime: the code runs under the restrictions notarization
	// requires.
	HardenedRuntime bool
	// Timestamp: the signature carries Apple's secure timestamp, without which
	// notarization refuses a submission.
	Timestamp bool

	// The two authorities above the leaf, each seen or not. ChainsToApple is
	// both of them.
	sawDeveloperIDCA, sawAppleRoot bool
}

// runtimeFlag finds the hardened runtime among codesign's flag set, which is
// printed as flags=0x10004(kill,runtime): the word, not the whole value.
var runtimeFlag = regexp.MustCompile(`(?m)^CodeDirectory .*\bflags=0x[0-9a-f]+\([^)]*\bruntime\b`)

func readSealFacts(display string) sealFacts {
	var f sealFacts
	for _, line := range strings.Split(display, "\n") {
		switch {
		case strings.HasPrefix(line, "Authority=Developer ID Application:"):
			f.DeveloperID = true
		case line == "Authority=Developer ID Certification Authority":
			f.sawDeveloperIDCA = true
		case line == "Authority=Apple Root CA":
			f.sawAppleRoot = true
		// "Timestamp=" is the secure one. A signature made with
		// --timestamp=none reports "Signed Time=" instead, and a substring
		// match would take that for the real thing.
		case strings.HasPrefix(line, "Timestamp="):
			f.Timestamp = true
		case strings.HasPrefix(line, "TeamIdentifier="):
			team := strings.TrimPrefix(line, "TeamIdentifier=")
			// codesign writes "not set" where there is no team. Read as text
			// that is a team name, and a check for a non-empty field passes.
			if team != "not set" {
				f.TeamID = team
			}
		}
	}
	f.HardenedRuntime = runtimeFlag.MatchString(display)
	f.ChainsToApple = f.sawDeveloperIDCA && f.sawAppleRoot
	return f
}

// SealReason names why a downloaded app was refused. The window turns it into
// a sentence in the person's own language, so it is a code and not a message.
type SealReason string

const (
	// SealBroken: the bundle's contents do not match its signature.
	SealBroken SealReason = "broken"
	// SealNotDeveloperID: signed by nobody -- ad hoc, or not at all.
	SealNotDeveloperID SealReason = "not-developer-id"
	// SealWrongTeam: signed by a real Apple developer who is not this one.
	SealWrongTeam SealReason = "wrong-team"
	// SealNoHardenedRuntime and SealNoTimestamp: signed in a way Apple would
	// not have notarized, so a ticket claiming otherwise is not to be believed.
	SealNoHardenedRuntime SealReason = "no-hardened-runtime"
	SealNoTimestamp       SealReason = "no-timestamp"
	// SealNotNotarized: Apple has not seen this build, or will not vouch for
	// it any more.
	SealNotNotarized SealReason = "not-notarized"
)

// SealError: a downloaded app was not the app it claims to be, and will not
// replace anything.
type SealError struct {
	Bundle string
	Reason SealReason
	// Want and Got carry the two team identifiers for SealWrongTeam.
	Want, Got string
	// Detail is what the tool said, for the log and for a person who wants it.
	Detail string
}

func (e *SealError) Error() string {
	switch e.Reason {
	case SealBroken:
		return "the downloaded app has been changed since it was signed: " + e.Detail
	case SealNotDeveloperID:
		return "the downloaded app is not signed with an Apple Developer ID, so there is no telling who built it"
	case SealWrongTeam:
		return "the downloaded app is signed by team " + e.Got + ", not " + e.Want
	case SealNoHardenedRuntime:
		return "the downloaded app was signed without the hardened runtime, which Apple requires before notarizing"
	case SealNoTimestamp:
		return "the downloaded app was signed without a secure timestamp, which Apple requires before notarizing"
	case SealNotNotarized:
		return "Apple has not notarized the downloaded app: " + e.Detail
	default:
		return "the downloaded app was refused: " + e.Detail
	}
}

// Seal is the check a downloaded app must pass before it is allowed to replace
// a running one.
//
// The three steps are three different questions, and no one of them answers
// another's -- each measured on 2026-09-12 against bundles built for the
// purpose:
//
//   - Is it whole? `codesign --verify --deep --strict`. It passes an ad-hoc
//     bundle with status 0, so on its own it says nothing about origin.
//   - Who signed it? `codesign --display`. On a bundle changed after signing
//     it still prints the original Developer ID, team and stapled ticket, so
//     on its own it is attacker-controlled.
//   - Would the system open it? `spctl --assess --type execute`. This is the
//     notarization check, and it is the reason nothing here calls `stapler`:
//     stapler lives inside Xcode (measured: /Applications/Xcode.app/Contents/
//     Developer/usr/bin/stapler), and this runs on a person's machine, which
//     need never have had Xcode on it. codesign and spctl are in /usr/bin and
//     /usr/sbin, part of macOS itself.
//
// The order is load-bearing: integrity first, because until it passes every
// other fact is whatever the archive says it is.
type Seal struct {
	// Codesign and Spctl are absolute paths -- /usr/bin/codesign and
	// /usr/sbin/spctl. Absolute, never looked up on PATH: this decides whether
	// an app replaces itself, and PATH is not something to trust with that.
	Codesign string
	Spctl    string
	// TeamID is the Apple team the new app must be signed by: the running
	// app's own, read with TeamIDOf, never a constant. A constant would stop
	// anybody else's fork from ever updating itself.
	TeamID string
}

// Verify refuses, with a reason, any bundle that is not this app signed by
// this team and notarized by Apple.
func (s *Seal) Verify(ctx context.Context, bundle string) error {
	if s.TeamID == "" {
		return fmt.Errorf("no team identifier to check %s against; refusing rather than accepting anything", bundle)
	}

	out, err := runTool(ctx, s.Codesign, "--verify", "--deep", "--strict", bundle)
	if err != nil {
		if !ranAndFailed(err) {
			return err
		}
		return &SealError{Bundle: bundle, Reason: SealBroken, Detail: firstLine(out)}
	}

	display, err := runTool(ctx, s.Codesign, "--display", "--verbose=4", bundle)
	if err != nil {
		return err
	}
	facts := readSealFacts(display)
	switch {
	case !facts.DeveloperID || !facts.ChainsToApple:
		return &SealError{Bundle: bundle, Reason: SealNotDeveloperID}
	case facts.TeamID != s.TeamID:
		return &SealError{Bundle: bundle, Reason: SealWrongTeam, Want: s.TeamID, Got: facts.TeamID}
	case !facts.HardenedRuntime:
		return &SealError{Bundle: bundle, Reason: SealNoHardenedRuntime}
	case !facts.Timestamp:
		return &SealError{Bundle: bundle, Reason: SealNoTimestamp}
	}

	assessment, err := runTool(ctx, s.Spctl, "--assess", "--type", "execute", "-vv", bundle)
	if err != nil {
		if !ranAndFailed(err) {
			return err
		}
		return &SealError{Bundle: bundle, Reason: SealNotNotarized, Detail: firstLine(assessment)}
	}
	// Accepted for some other reason -- a rule somebody added on this machine,
	// say -- is not the same as accepted because Apple vouched for it.
	if !strings.Contains(assessment, "source=Notarized Developer ID") {
		return &SealError{Bundle: bundle, Reason: SealNotNotarized, Detail: firstLine(assessment)}
	}
	return nil
}

// TeamIDOf is the Apple team a bundle is signed by.
func TeamIDOf(ctx context.Context, codesign, bundle string) (string, error) {
	out, err := runTool(ctx, codesign, "--display", "--verbose=4", bundle)
	if err != nil {
		return "", err
	}
	team := readSealFacts(out).TeamID
	if team == "" {
		return "", fmt.Errorf("%s names no Apple team: it is not signed with a Developer ID", bundle)
	}
	return team, nil
}

// runTool runs one of the system's signing tools. Both write what they have to
// say to standard error, so the two streams are read as one.
func runTool(ctx context.Context, tool string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	if err != nil && !ranAndFailed(err) {
		return string(out), fmt.Errorf("run %s: %w", tool, err)
	}
	return string(out), err
}

// ranAndFailed tells a tool that ran and said no from a tool that could not be
// run at all. The difference matters: the first is an answer about the bundle,
// the second is a broken machine, and reporting the second as "this app is not
// signed" would be a lie.
func ranAndFailed(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
