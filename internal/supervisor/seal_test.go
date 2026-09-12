package supervisor

import (
	"strings"
	"testing"
)

// The three outputs below are `codesign --display --verbose=4` as it was
// measured on 2026-09-12, on the operator's machine, against three bundles
// built from the same app: the notarized v0.3.0 in /Applications, a copy of it
// re-signed ad hoc, and a copy with one byte appended to its icon. They are
// kept verbatim rather than shortened, because what this parser must not do is
// find what it wants in a line that is not the line it wants.

const notarizedDisplay = `Executable=/Applications/fleetdeck.app/Contents/MacOS/fleetdeck-window
Identifier=dev.fleetdeck.window
Format=app bundle with Mach-O universal (x86_64 arm64)
CodeDirectory v=20500 size=20352 flags=0x10000(runtime) hashes=625+7 location=embedded
Hash type=sha256 size=32
CDHash=16f6999b8a788b83f708ebbb9479cded68f731ab
Signature size=8977
Authority=Developer ID Application: Denis Davydov (PTLLPQ8LY4)
Authority=Developer ID Certification Authority
Authority=Apple Root CA
Timestamp=12 Sep 2026 at 11:00:29
Notarization Ticket=stapled
Info.plist entries=11
TeamIdentifier=PTLLPQ8LY4
Runtime Version=26.5.0
Sealed Resources version=2 rules=13 files=3
Internal requirements count=1 size=180`

const adhocDisplay = `Executable=/tmp/adhoc.app/Contents/MacOS/fleetdeck-window
Identifier=dev.fleetdeck.window
Format=app bundle with Mach-O universal (x86_64 arm64)
CodeDirectory v=20400 size=20205 flags=0x2(adhoc) hashes=625+3 location=embedded
Hash type=sha256 size=32
CDHash=0bd4e2f18a1e5e8e2c9b0c9d8e7f6a5b4c3d2e1f
Signature=adhoc
Info.plist entries=11
TeamIdentifier=not set
Sealed Resources version=2 rules=13 files=3
Internal requirements count=1 size=180`

func TestSealFactsReadANotarizedDeveloperIDSignature(t *testing.T) {
	f := readSealFacts(notarizedDisplay)

	if !f.DeveloperID {
		t.Error("did not see a Developer ID Application authority")
	}
	if !f.ChainsToApple {
		t.Error("did not see the chain to Apple's Developer ID authority and root")
	}
	if f.TeamID != "PTLLPQ8LY4" {
		t.Errorf("team identifier %q, want PTLLPQ8LY4", f.TeamID)
	}
	if !f.HardenedRuntime {
		t.Error("did not see the hardened runtime flag")
	}
	if !f.Timestamp {
		t.Error("did not see the secure timestamp")
	}
}

// The ad-hoc seal is what every build without a certificate gets -- a
// developer's own, and CI's. It is a valid signature that identifies nobody,
// and every one of these has to come back false.
func TestSealFactsReadAnAdHocSignatureAsIdentifyingNobody(t *testing.T) {
	f := readSealFacts(adhocDisplay)

	if f.DeveloperID {
		t.Error("read an ad-hoc signature as a Developer ID one")
	}
	if f.ChainsToApple {
		t.Error("read an ad-hoc signature as chaining to Apple")
	}
	if f.HardenedRuntime {
		t.Error("read flags=0x2(adhoc) as the hardened runtime")
	}
	if f.Timestamp {
		t.Error("read an ad-hoc signature as carrying a secure timestamp")
	}
}

// "TeamIdentifier=not set" is codesign's way of saying there is none. Read as
// text it is a team called "not set", and a check that only asked whether the
// field was non-empty would let it through.
func TestSealFactsDoNotReadNotSetAsATeam(t *testing.T) {
	if got := readSealFacts(adhocDisplay).TeamID; got != "" {
		t.Fatalf("team identifier %q, want it empty: codesign said \"not set\"", got)
	}
}

// codesign reports a signature made without --timestamp as "Signed Time=",
// and one with the secure timestamp as "Timestamp=". A substring match on
// "Timestamp=" finds both, which is exactly the mistake this pins.
func TestSealFactsDoNotReadASignedTimeAsASecureTimestamp(t *testing.T) {
	display := strings.Replace(notarizedDisplay,
		"Timestamp=12 Sep 2026 at 11:00:29",
		"Signed Time=12 Sep 2026 at 11:00:29", 1)

	if readSealFacts(display).Timestamp {
		t.Fatal("read \"Signed Time=\" as a secure timestamp; notarization refuses a submission without one")
	}
}

// The hardened runtime is one flag among several, and the flags are printed as
// a set: flags=0x10002(adhoc,runtime) is a thing codesign prints. Matching the
// word inside the brackets is what this has to do, not matching the whole
// value.
func TestSealFactsFindTheRuntimeFlagAmongOthers(t *testing.T) {
	display := strings.Replace(notarizedDisplay,
		"flags=0x10000(runtime)",
		"flags=0x10004(kill,runtime)", 1)

	if !readSealFacts(display).HardenedRuntime {
		t.Fatal("did not find the runtime flag beside another one")
	}
}

// An Authority line for something that merely mentions a Developer ID -- an
// intermediate, or a certificate somebody named that way -- is not the leaf
// authority this looks for.
func TestSealFactsDoNotTakeAnyAuthorityMentioningDeveloperID(t *testing.T) {
	display := strings.Replace(notarizedDisplay,
		"Authority=Developer ID Application: Denis Davydov (PTLLPQ8LY4)",
		"Authority=Some Other Developer ID Application thing", 1)

	if readSealFacts(display).DeveloperID {
		t.Fatal("took an authority that only mentions a Developer ID for the leaf one")
	}
}
