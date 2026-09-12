//go:build darwin

package supervisor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// installedApp is the app these tests ask for a real Developer ID signature.
// The cases that need one skip when it is not there or is not signed that way:
// a machine with no fleetdeck installed, and CI, have nothing to measure
// against, and a test that invented a certificate would be measuring itself.
const installedApp = "/Applications/fleetdeck.app"

func realSeal(teamID string) *Seal {
	return &Seal{Codesign: "/usr/bin/codesign", Spctl: "/usr/sbin/spctl", TeamID: teamID}
}

// adhocBundle builds a bundle shaped like an app and seals it ad hoc -- what
// every build without a certificate gets, and what an attacker's own build of
// this app would look like. /bin/echo stands in for the binaries: nothing here
// runs it.
func adhocBundle(t *testing.T) string {
	t.Helper()
	app := filepath.Join(t.TempDir(), "probe.app")
	macos := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macos, 0o755); err != nil {
		t.Fatal(err)
	}
	echo, err := os.ReadFile("/bin/echo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(macos, "probe"), echo, 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>probe</string>
<key>CFBundleIdentifier</key><string>dev.fleetdeck.sealprobe</string>
<key>CFBundleName</key><string>probe</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	// A bundle identifier of its own, never the app's: LaunchServices
	// registers every bundle it is shown by identifier, and a stand-in wearing
	// the real app's identifier makes system notices speak in its name (see
	// docs/engineering/release-app.md section 6).
	out, err := exec.Command("/usr/bin/codesign", "--force", "--sign", "-", app).CombinedOutput()
	if err != nil {
		t.Fatalf("seal the probe bundle ad hoc: %v\n%s", err, out)
	}
	return app
}

// developerIDBundle is a copy of the installed app, or a skip.
func developerIDBundle(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(installedApp); err != nil {
		t.Skipf("no app at %s to measure a real signature against", installedApp)
	}
	out, err := exec.Command("/usr/bin/codesign", "--display", "--verbose=4", installedApp).CombinedOutput()
	if err != nil || !readSealFacts(string(out)).DeveloperID {
		t.Skipf("%s is not signed with a Developer ID; nothing to measure", installedApp)
	}
	copied := filepath.Join(t.TempDir(), "copy.app")
	if out, err := exec.Command("/usr/bin/ditto", installedApp, copied).CombinedOutput(); err != nil {
		t.Fatalf("copy %s: %v\n%s", installedApp, err, out)
	}
	return copied
}

func teamIDOfInstalledApp(t *testing.T) string {
	t.Helper()
	out, _ := exec.Command("/usr/bin/codesign", "--display", "--verbose=4", installedApp).CombinedOutput()
	return readSealFacts(string(out)).TeamID
}

// The one case that must pass: the app as a release actually ships it.
func TestVerifyAcceptsTheNotarizedReleaseApp(t *testing.T) {
	app := developerIDBundle(t)

	if err := realSeal(teamIDOfInstalledApp(t)).Verify(context.Background(), app); err != nil {
		t.Fatalf("the real signed app was refused: %v", err)
	}
}

// An ad-hoc seal is a valid signature that identifies nobody, and it is what
// anyone can put on anything. Measured on 2026-09-12: `codesign --verify
// --deep --strict` passes such a bundle with status 0, so integrity alone is
// not a check on where an app came from.
func TestVerifyRefusesAnAdHocBundle(t *testing.T) {
	app := adhocBundle(t)

	err := realSeal("PTLLPQ8LY4").Verify(context.Background(), app)

	var sealErr *SealError
	if !errors.As(err, &sealErr) {
		t.Fatalf("got %v, want a *SealError", err)
	}
	if sealErr.Reason != SealNotDeveloperID {
		t.Fatalf("refused for %q, want %q", sealErr.Reason, SealNotDeveloperID)
	}
}

// The integrity check has to come first, and this is why: `codesign --display`
// on a bundle changed after signing still prints the original certificate, the
// original team identifier and "Notarization Ticket=stapled". Every identity
// fact is attacker-controlled until the seal has been verified.
func TestVerifyRefusesABundleChangedAfterItWasSigned(t *testing.T) {
	app := adhocBundle(t)
	stray := filepath.Join(app, "Contents", "MacOS", "extra")
	if err := os.WriteFile(stray, []byte("not sealed"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := realSeal("PTLLPQ8LY4").Verify(context.Background(), app)

	var sealErr *SealError
	if !errors.As(err, &sealErr) {
		t.Fatalf("got %v, want a *SealError", err)
	}
	if sealErr.Reason != SealBroken {
		t.Fatalf("refused for %q, want %q: integrity is checked before identity", sealErr.Reason, SealBroken)
	}
}

// A properly signed, properly notarized app from somebody else is the attack
// this check exists for: everything Gatekeeper asks about it comes back yes.
// Only the team identifier says it is not ours.
func TestVerifyRefusesAnAppSignedByAnotherTeam(t *testing.T) {
	app := developerIDBundle(t)

	err := realSeal("ZZZZZZZZZZ").Verify(context.Background(), app)

	var sealErr *SealError
	if !errors.As(err, &sealErr) {
		t.Fatalf("got %v, want a *SealError", err)
	}
	if sealErr.Reason != SealWrongTeam {
		t.Fatalf("refused for %q, want %q", sealErr.Reason, SealWrongTeam)
	}
	if !strings.Contains(sealErr.Error(), "ZZZZZZZZZZ") {
		t.Errorf("the refusal does not name the team that was expected: %v", sealErr)
	}
}

// standInSpctl writes a script that answers like spctl with the given status
// and text. Standing the tool in is the only way to reach this case: making
// the real spctl accept an app for a reason other than notarization means
// adding a rule to the machine's security policy, which no test may do.
func standInSpctl(t *testing.T, status int, say string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spctl")
	script := "#!/bin/sh\ncat >/dev/null 2>&1 <<'EOF'\nEOF\nprintf '%s\\n' " +
		strconv.Quote(say) + "\nexit " + strconv.Itoa(status) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Gatekeeper accepting an app is not the same as Apple having notarized it: a
// rule added to the machine -- by an administrator, by something a person once
// allowed -- makes spctl answer "accepted" with a different source line. The
// question this asks is "did Apple vouch for this", and only one answer means
// yes.
func TestVerifyRefusesAnAppAcceptedForSomeOtherReasonThanNotarization(t *testing.T) {
	app := developerIDBundle(t)
	seal := realSeal(teamIDOfInstalledApp(t))
	seal.Spctl = standInSpctl(t, 0, app+": accepted\nsource=Developer ID\noverride=whitelist")

	err := seal.Verify(context.Background(), app)

	var sealErr *SealError
	if !errors.As(err, &sealErr) {
		t.Fatalf("got %v, want a *SealError: accepted by a local rule is not notarized", err)
	}
	if sealErr.Reason != SealNotNotarized {
		t.Fatalf("refused for %q, want %q", sealErr.Reason, SealNotNotarized)
	}
}

// The refusal a person is likeliest to meet if they are handed a signed but
// unnotarized build: status 3, with the source line saying so.
func TestVerifyRefusesAnUnnotarizedDeveloperIDApp(t *testing.T) {
	app := developerIDBundle(t)
	seal := realSeal(teamIDOfInstalledApp(t))
	seal.Spctl = standInSpctl(t, 3, app+": rejected\nsource=Unnotarized Developer ID")

	err := seal.Verify(context.Background(), app)

	var sealErr *SealError
	if !errors.As(err, &sealErr) {
		t.Fatalf("got %v, want a *SealError", err)
	}
	if sealErr.Reason != SealNotNotarized {
		t.Fatalf("refused for %q, want %q", sealErr.Reason, SealNotNotarized)
	}
	if !strings.Contains(sealErr.Error(), "rejected") {
		t.Errorf("the refusal does not carry what the system said: %v", sealErr)
	}
}

// The attack this feature exists to stop, on a real signature rather than a
// made-up team: an app signed with a certificate belonging to somebody else.
// Everything about it is genuine -- a real Apple certificate, a real
// timestamp, the hardened runtime -- and `codesign --verify` passes it with
// status 0. Only the team says it is not ours.
//
// The bundle is signed outside the test, because signing needs a private key
// this machine may not have and may ask a person for:
//
//	ditto /Applications/fleetdeck.app /tmp/otherteam.app
//	codesign --force --sign "<some other identity>" --options runtime \
//	    --timestamp --deep /tmp/otherteam.app
//	FLEETDECK_OTHER_TEAM_APP=/tmp/otherteam.app go test ./internal/supervisor/
func TestVerifyRefusesARealSignatureFromAnotherTeam(t *testing.T) {
	app := os.Getenv("FLEETDECK_OTHER_TEAM_APP")
	if app == "" {
		t.Skip("set FLEETDECK_OTHER_TEAM_APP to a bundle signed by a different Apple team")
	}
	ours := teamIDOfInstalledApp(t)
	theirs, err := TeamIDOf(context.Background(), "/usr/bin/codesign", app)
	if err != nil {
		t.Fatal(err)
	}
	if theirs == ours {
		t.Fatalf("%s is signed by our own team %s; there is nothing to measure", app, ours)
	}

	err = realSeal(ours).Verify(context.Background(), app)

	var sealErr *SealError
	if !errors.As(err, &sealErr) {
		t.Fatalf("an app signed by team %s was accepted for team %s: %v", theirs, ours, err)
	}
	t.Logf("refused an app signed by team %s: %s", theirs, sealErr.Reason)
}

// TeamIDOf is how the window learns which team to demand: its own. Hard-coding
// one would stop anyone else's fork from updating itself.
func TestTeamIDOfReadsTheTeamAnAppIsSignedBy(t *testing.T) {
	app := developerIDBundle(t)

	got, err := TeamIDOf(context.Background(), "/usr/bin/codesign", app)
	if err != nil {
		t.Fatal(err)
	}
	if got != teamIDOfInstalledApp(t) {
		t.Fatalf("read team %q, want %q", got, teamIDOfInstalledApp(t))
	}
}

// A build with no Developer ID has no team to demand, and that has to be a
// refusal with a name rather than an empty string quietly matching another
// empty string -- which would turn "we cannot tell who signed this" into
// "everyone is allowed".
func TestTeamIDOfRefusesABuildThatNamesNoTeam(t *testing.T) {
	app := adhocBundle(t)

	_, err := TeamIDOf(context.Background(), "/usr/bin/codesign", app)
	if err == nil {
		t.Fatal("an ad-hoc bundle was read as naming a team")
	}
}

// An empty expected team must never verify anything, whatever the bundle:
// the zero value of a struct field cannot be the thing that opens the door.
func TestVerifyRefusesWhenItHasNoTeamToDemand(t *testing.T) {
	app := developerIDBundle(t)

	if err := realSeal("").Verify(context.Background(), app); err == nil {
		t.Fatal("a Seal with no TeamID verified a bundle")
	}
}

// codesign and spctl are in /usr/bin and /usr/sbin on every macOS, so this is
// not expected to happen -- which is exactly why it must not come out as a
// silence or a pass.
func TestVerifySaysWhenTheToolIsNotThere(t *testing.T) {
	app := adhocBundle(t)
	seal := &Seal{Codesign: "/nowhere/codesign", Spctl: "/nowhere/spctl", TeamID: "PTLLPQ8LY4"}

	err := seal.Verify(context.Background(), app)
	if err == nil {
		t.Fatal("a missing codesign was not reported at all")
	}
	if !strings.Contains(err.Error(), "/nowhere/codesign") {
		t.Errorf("the error does not name the tool it could not run: %v", err)
	}
}
