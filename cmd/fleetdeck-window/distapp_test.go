//go:build darwin

package main

import (
	"bytes"
	"debug/buildinfo"
	"debug/macho"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// releaseAppVersion is the tag the release app under test is built for. Not "dev":
// "dev" is what a binary says when the version never reached it, so a test built
// on it would pass against a release that carries no version at all.
const releaseAppVersion = "v0.1.0"

// wantReleaseAppMembers is everything the release zip may hold: the bundle, the
// three commands, the icon, the plist, and the seal over them. No AppleDouble "._"
// companions, no __MACOSX, no staging directory.
var wantReleaseAppMembers = []string{
	"fleetdeck.app/",
	"fleetdeck.app/Contents/",
	"fleetdeck.app/Contents/Info.plist",
	"fleetdeck.app/Contents/MacOS/",
	"fleetdeck.app/Contents/MacOS/fleetdeck",
	"fleetdeck.app/Contents/MacOS/fleetdeck-status",
	"fleetdeck.app/Contents/MacOS/fleetdeck-window",
	"fleetdeck.app/Contents/Resources/",
	"fleetdeck.app/Contents/Resources/icon.icns",
	"fleetdeck.app/Contents/_CodeSignature/",
	"fleetdeck.app/Contents/_CodeSignature/CodeResources",
}

// The ldflags a release slice must carry, exactly: the version and nothing else.
// In particular no main.treeDir -- a release is built on a CI runner, and a
// window that knew the runner's checkout would show an Update button pointing at
// a tree that exists on no machine the app is installed on.
const wantReleaseLdflags = `-ldflags="-X github.com/kroticw/fleetdeck/internal/version.value=` + releaseAppVersion + `"`

// TestDistAppBuildsAnAppAPersonCanInstall runs the real `make dist-app` and
// interrogates the zip it leaves, the way a person gets it: unpacked, then
// looked at from the outside. It trusts nothing the target says about itself --
// the target's own verification is one edit away from checking nothing, and what
// it blesses is published.
func TestDistAppBuildsAnAppAPersonCanInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the whole universal app bundle")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	distDir := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A zip from another version would be uploaded under this tag by the publish
	// step's glob. The tarball beside it is `make dist`'s, which the release runs
	// first into the same directory: dist-app must not take it with it.
	stale := filepath.Join(distDir, "fleetdeck-v0.0.1-macos.zip")
	tarball := filepath.Join(distDir, "fleetdeck-"+releaseAppVersion+"-darwin-arm64.tar.gz")
	for _, f := range []string{stale, tarball} {
		if err := os.WriteFile(f, []byte("not this build's zip"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// SIGN_IDENTITY is emptied on the command line, where make lets nothing
	// override it, rather than left to the environment: this test is about the
	// build every machine without a certificate makes -- a developer's, and CI's
	// check job -- and it must measure that same build on the one machine that
	// does have a certificate and may well have the variable exported.
	cmd := exec.Command("make", "dist-app", "VERSION="+releaseAppVersion, "DISTDIR="+distDir, "SIGN_IDENTITY=")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make dist-app: %v\n%s", err, out)
	}
	zip := filepath.Join(distDir, "fleetdeck-"+releaseAppVersion+"-macos.zip")
	// The release workflow's gate is verify-dist-app, and a gate that stops part
	// way with status 0 passes everything after the point it stopped. That is not
	// hypothetical: a case inside $(...), which the bash 3.2 that is /bin/sh on
	// macOS cannot parse, once ended it early, silently, with every check after
	// the architectures unrun. Its last line is the proof it got to the end.
	if !strings.Contains(string(out), "verify-dist-app: "+zip+" ok") {
		t.Fatalf("make dist-app succeeded without verify-dist-app reaching its end:\n%s", out)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("%s survived make dist-app and would be published under %s", stale, releaseAppVersion)
	}
	if _, err := os.Stat(tarball); err != nil {
		t.Fatalf("make dist-app removed make dist's archive %s: %v", tarball, err)
	}
	zips, err := filepath.Glob(filepath.Join(distDir, "*.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if len(zips) != 1 || zips[0] != zip {
		t.Fatalf("make dist-app must leave exactly %s, found %v", zip, zips)
	}

	listing, err := exec.Command("unzip", "-Z1", zip).Output()
	if err != nil {
		t.Fatalf("unzip -Z1 %s: %v", zip, err)
	}
	members := strings.Fields(string(listing))
	sort.Strings(members)
	if !reflect.DeepEqual(members, wantReleaseAppMembers) {
		t.Fatalf("%s must hold exactly the app:\nwant %v\ngot  %v", zip, wantReleaseAppMembers, members)
	}

	// ditto is what Finder's Archive Utility is built on; unzip would do too.
	unpacked := t.TempDir()
	if out, err := exec.Command("ditto", "-x", "-k", zip, unpacked).CombinedOutput(); err != nil {
		t.Fatalf("ditto -x -k %s: %v\n%s", zip, err, out)
	}
	app := filepath.Join(unpacked, "fleetdeck.app")
	window := filepath.Join(app, "Contents", "MacOS", "fleetdeck-window")

	t.Run("the plist carries the tag and is window-app's in every other key", func(t *testing.T) {
		got := plistKeys(t, filepath.Join(app, "Contents", "Info.plist"))
		want := plistKeys(t, "Info.plist") // what `make window-app` copies as it is
		for _, key := range []string{"CFBundleShortVersionString", "CFBundleVersion"} {
			if got[key] != strings.TrimPrefix(releaseAppVersion, "v") {
				t.Errorf("%s = %v, want %q: Finder's Get Info would show a version this release is not", key, got[key], strings.TrimPrefix(releaseAppVersion, "v"))
			}
			delete(got, key)
			delete(want, key)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the release plist drifted from window-app's:\nrelease    %v\nwindow-app %v", got, want)
		}
	})

	t.Run("the icon is the one window-app ships", func(t *testing.T) {
		got, err := os.ReadFile(filepath.Join(app, "Contents", "Resources", "icon.icns"))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile("icon.icns")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Error("the release icon is not cmd/fleetdeck-window/icon.icns")
		}
	})

	t.Run("every command runs on both architectures with the release version", func(t *testing.T) {
		for _, bin := range []string{window, panelBinary(window), filepath.Join(filepath.Dir(window), "fleetdeck-status")} {
			slices := fatSlices(t, bin)
			var cpus []string
			for cpu := range slices {
				cpus = append(cpus, cpu)
			}
			sort.Strings(cpus)
			if strings.Join(cpus, " ") != "amd64 arm64" {
				t.Errorf("%s holds %v, want both amd64 and arm64: half the Macs could not open it", filepath.Base(bin), cpus)
			}
			for cpu, s := range slices {
				if s.goarch != cpu {
					t.Errorf("%s: the %s slice was built for GOARCH=%s", filepath.Base(bin), cpu, s.goarch)
				}
				if s.ldflags != wantReleaseLdflags {
					t.Errorf("%s (%s) was built with %s, want %s", filepath.Base(bin), cpu, s.ldflags, wantReleaseLdflags)
				}
				// The window is WebKit or it is nothing; the panel must not be a
				// second copy of it. Checked per slice: a cross-built slice is
				// exactly where cgo quietly targets the wrong thing.
				if isWindow := bin == window; s.webkit != isWindow {
					t.Errorf("%s (%s) links WebKit = %v, want %v", filepath.Base(bin), cpu, s.webkit, isWindow)
				}
			}
		}
	})

	t.Run("the seal covers the whole bundle", func(t *testing.T) {
		// Without it the bundle's signature is the linker's, over the window
		// binary alone, and a quarantined copy fails Gatekeeper's signature
		// check instead of reaching the question a person can answer.
		if out, err := exec.Command("codesign", "--verify", "--deep", "--strict", app).CombinedOutput(); err != nil {
			t.Fatalf("codesign --verify --deep --strict: %v\n%s", err, out)
		}
	})

	t.Run("a build given no identity says so in the signature", func(t *testing.T) {
		// The point is not that ad hoc is good -- it is that an unsigned build
		// must be visibly unsigned. A release must never leave this state
		// (the workflow's gate demands `notarized`), and the way to be sure the
		// two builds are really different builds is to check that this one
		// carries neither a Developer ID nor the hardened runtime.
		info := sealOf(t, app)
		if !strings.Contains(info, "Signature=adhoc") {
			t.Errorf("a build with no SIGN_IDENTITY is sealed with something other than an ad-hoc signature:\n%s", info)
		}
		if strings.Contains(info, "Authority=Developer ID Application") {
			t.Error("a build with no SIGN_IDENTITY picked up a Developer ID certificate from somewhere")
		}
		if strings.Contains(info, "(runtime)") {
			t.Error("a build with no SIGN_IDENTITY claims the hardened runtime, which nothing has measured it under")
		}
	})

	t.Run("the panel beside the window reports the tag", func(t *testing.T) {
		out, err := exec.Command(panelBinary(window), "version").CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != releaseAppVersion {
			t.Fatalf("%s version = %q (%v), want %q", panelBinary(window), out, err, releaseAppVersion)
		}
	})

	t.Run("setup finds the status reporter where it looks", func(t *testing.T) {
		// cmd/fleetdeck's first-run setup wires Claude Code's statusline to the
		// fleetdeck-status beside the running panel, and writes nothing when it
		// is not there. A release without it installs a fleet with no statusline.
		info, err := os.Stat(filepath.Join(filepath.Dir(panelBinary(window)), "fleetdeck-status"))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("no executable fleetdeck-status beside the panel: %v", err)
		}
	})

	// The disk image is built out of the zip above, so it belongs to this test
	// rather than to one of its own: a separate test would have to build the
	// whole universal app a second time to have a zip to build an image from,
	// and `make test` runs this on every pull request.
	diskImageBuiltFromTheZip(t, root, distDir)
}

// diskImageBuiltFromTheZip runs the real `make dist-dmg` against the dist
// directory the zip is already in, and interrogates the image it leaves the way
// a person gets it: mounted, then looked at from the outside. As above, it
// trusts nothing the target says about itself.
func diskImageBuiltFromTheZip(t *testing.T, root, distDir string) {
	t.Helper()
	zip := filepath.Join(distDir, "fleetdeck-"+releaseAppVersion+"-macos.zip")
	// An image from another version would be uploaded under this tag by the
	// publish step's glob, exactly as a stale zip would.
	stale := filepath.Join(distDir, "fleetdeck-v0.0.1-macos.dmg")
	if err := os.WriteFile(stale, []byte("not this build's image"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("make", "dist-dmg", "VERSION="+releaseAppVersion, "DISTDIR="+distDir, "SIGN_IDENTITY=")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make dist-dmg: %v\n%s", err, out)
	}
	dmg := filepath.Join(distDir, "fleetdeck-"+releaseAppVersion+"-macos.dmg")
	// Its last line is the proof the gate got to the end, for the same reason
	// the zip's is: a gate that stops part way with status 0 passes everything
	// after the point it stopped.
	if !strings.Contains(string(out), "verify-dist-dmg: "+dmg+" ok") {
		t.Fatalf("make dist-dmg succeeded without verify-dist-dmg reaching its end:\n%s", out)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("%s survived make dist-dmg and would be published under %s", stale, releaseAppVersion)
	}
	if _, err := os.Stat(zip); err != nil {
		t.Fatalf("make dist-dmg removed the zip the release also publishes: %v", err)
	}
	images, err := filepath.Glob(filepath.Join(distDir, "*.dmg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0] != dmg {
		t.Fatalf("make dist-dmg must leave exactly %s, found %v", dmg, images)
	}

	mounted := mountImage(t, dmg)

	t.Run("the volume holds the app, the shortcut and the window and nothing else", func(t *testing.T) {
		entries, err := os.ReadDir(mounted)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		want := []string{".DS_Store", ".background", "Applications", "fleetdeck.app"}
		if !reflect.DeepEqual(names, want) {
			t.Fatalf("the volume must hold exactly %v, found %v", want, names)
		}
	})

	t.Run("Applications is a shortcut to Applications", func(t *testing.T) {
		// A folder by that name would be a place to drop the app that goes
		// nowhere, and a copied /Applications would be a 60 GB image.
		target, err := os.Readlink(filepath.Join(mounted, "Applications"))
		if err != nil {
			t.Fatalf("Applications on the volume is not a symbolic link: %v", err)
		}
		if target != "/Applications" {
			t.Fatalf("Applications on the volume points at %q, want /Applications", target)
		}
	})

	t.Run("the window published is the window committed", func(t *testing.T) {
		// The layout cannot be computed -- Finder writes it -- so what this can
		// check is that the bytes shipped are the bytes reviewed. See
		// scripts/build-dmg-layout.sh.
		for _, f := range []struct{ onImage, inRepo string }{
			{filepath.Join(mounted, ".DS_Store"), filepath.Join(root, "packaging", "dmg", "DS_Store")},
			{filepath.Join(mounted, ".background", "background.tiff"), filepath.Join(root, "packaging", "dmg", "background.tiff")},
		} {
			shipped, err := os.ReadFile(f.onImage)
			if err != nil {
				t.Fatalf("reading %s: %v", f.onImage, err)
			}
			committed, err := os.ReadFile(f.inRepo)
			if err != nil {
				t.Fatalf("reading %s: %v", f.inRepo, err)
			}
			if !bytes.Equal(shipped, committed) {
				t.Fatalf("%s on the image is not %s", filepath.Base(f.onImage), f.inRepo)
			}
		}
	})

	t.Run("the app on the image reports the tag", func(t *testing.T) {
		// Asked of the bundle in the place a person would drag it from, and by
		// running it rather than by reading its metadata: the linker accepts an
		// -X target that does not exist without complaint.
		panel := filepath.Join(mounted, "fleetdeck.app", "Contents", "MacOS", "fleetdeck")
		out, err := exec.Command(panel, "version").CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != releaseAppVersion {
			t.Fatalf("%s version = %q (%v), want %q", panel, out, err, releaseAppVersion)
		}
	})
}

// mountImage attaches dmg read-only and out of sight, and detaches it when the
// test ends however it ends. A mounted image outlives the process that mounted
// it, and a test that left one behind would leave the next run to find the
// volume name already taken.
func mountImage(t *testing.T, dmg string) string {
	t.Helper()
	// -mountrandom needs the directory it randomises inside to exist already;
	// without it hdiutil fails with "no mountable file systems", which reads
	// like a broken image and is not one.
	into := t.TempDir()
	out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-mountrandom", into, "-plist", dmg).Output()
	if err != nil {
		t.Fatalf("hdiutil attach %s: %v", dmg, err)
	}
	mounted := mountPointIn(t, string(out))
	t.Cleanup(func() {
		if out, err := exec.Command("hdiutil", "detach", mounted, "-force").CombinedOutput(); err != nil {
			t.Errorf("hdiutil detach %s: %v\n%s", mounted, err, out)
		}
	})
	return mounted
}

// mountPointIn pulls the mount point out of hdiutil's plist without a plist
// decoder: the value is the one <string> under a <key>mount-point</key>.
func mountPointIn(t *testing.T, attachPlist string) string {
	t.Helper()
	const key = "<key>mount-point</key>"
	i := strings.Index(attachPlist, key)
	if i < 0 {
		t.Fatalf("hdiutil said where nothing was mounted:\n%s", attachPlist)
	}
	rest := attachPlist[i+len(key):]
	open := strings.Index(rest, "<string>")
	closing := strings.Index(rest, "</string>")
	if open < 0 || closing < open {
		t.Fatalf("hdiutil's mount point is not a string:\n%s", attachPlist)
	}
	return strings.TrimSpace(rest[open+len("<string>") : closing])
}

// plistKeys reads a property list as a map, through plutil's JSON form.
func plistKeys(t *testing.T, path string) map[string]any {
	t.Helper()
	out, err := exec.Command("plutil", "-convert", "json", "-o", "-", path).Output()
	if err != nil {
		t.Fatalf("plutil %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("plutil %s: %v", path, err)
	}
	return m
}

type slice struct {
	goarch  string
	ldflags string
	webkit  bool
}

// fatSlices reads every architecture slice of a universal binary on its own.
// `go version -m` is not enough here: on a universal binary it reads the first
// slice only and says nothing about the rest (measured on 2026-09-11 with go
// 1.27.1: an x86_64+arm64 binary reported GOARCH=amd64 alone).
func fatSlices(t *testing.T, path string) map[string]slice {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fat, err := macho.NewFatFile(f)
	if err != nil {
		t.Fatalf("%s is not a universal binary: %v", filepath.Base(path), err)
	}
	slices := map[string]slice{}
	for _, arch := range fat.Arches {
		cpu := map[macho.Cpu]string{macho.CpuArm64: "arm64", macho.CpuAmd64: "amd64"}[arch.Cpu]
		if cpu == "" {
			cpu = arch.Cpu.String()
		}
		var s slice
		info, err := buildinfo.Read(io.NewSectionReader(f, int64(arch.Offset), int64(arch.Size)))
		if err != nil {
			t.Fatalf("%s (%s): no Go build record: %v", filepath.Base(path), cpu, err)
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "GOARCH":
				s.goarch = setting.Value
			case "-ldflags":
				s.ldflags = `-ldflags="` + setting.Value + `"`
			}
		}
		for _, l := range arch.Loads {
			if d, ok := l.(*macho.Dylib); ok && strings.Contains(d.Name, "WebKit.framework") {
				s.webkit = true
			}
		}
		slices[cpu] = s
	}
	return slices
}

// sealOf reads what codesign says about a bundle or a binary. --display writes
// to standard error, which is why this is CombinedOutput and not Output.
func sealOf(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("codesign", "--display", "--verbose=4", path).CombinedOutput()
	if err != nil {
		t.Fatalf("codesign --display %s: %v\n%s", path, err, out)
	}
	return string(out)
}

// wantEntitlements is every exception to the hardened runtime the release app may
// ask for. It is empty, and that is a result, not an omission: on 2026-09-12 a
// probe signed exactly the way a release is -- Developer ID, --options runtime,
// this very file -- did from inside a bundle each thing the app does, and every
// one of them was allowed with no entitlement at all: the osascript banner
// internal/notify sends, starting a system binary, starting the panel beside it
// in the bundle, listening on a loopback port, and reading the home directory.
// The system log was watched throughout, with a control event in it to prove the
// watcher was not blind, and it recorded no denial.
//
// The list freshman-desktop keeps is four entries long and none of them belongs
// here. allow-jit and allow-unsigned-executable-memory are Chromium's: V8
// compiles in the browser process itself. This window is WKWebView, whose
// JavaScript runs in Apple's own WebContent process under Apple's own signature.
// disable-library-validation is for loading native modules built by somebody
// else; this app links system frameworks and nothing more, which
// TestWindowBinaryLinksAgainstWebKit already holds it to. And network.client is
// an App Sandbox entitlement, which means nothing outside a sandbox.
//
// Every entry added here is a hole in the hardened runtime, and a hole opened by
// copying someone else's list is one nobody measured. Add one when a measurement
// says the app is denied without it, and write down which denial.
var wantEntitlements = map[string]any{}

// TestTheReleaseAppAsksForNoHardenedRuntimeExceptionsItHasNotMeasured holds the
// file the release is signed with to the list above. A signature weakened by an
// extra entitlement still verifies, still notarizes and still opens: nothing
// downstream of this test would notice.
func TestTheReleaseAppAsksForNoHardenedRuntimeExceptionsItHasNotMeasured(t *testing.T) {
	got := plistKeys(t, "entitlements.plist")
	if !reflect.DeepEqual(got, wantEntitlements) {
		t.Errorf("entitlements.plist asks for exceptions this app has not been measured to need:\nfile %v\nwant %v", got, wantEntitlements)
	}
}

// TestABuildToldToSignWithAnIdentityItHasNotStops covers the other way a release
// can go quietly wrong: the workflow sets SIGN_IDENTITY from a secret, and a
// secret that is empty, renamed or misspelt would otherwise build an ad-hoc app
// and carry it happily to the gate. The build refuses instead, and refuses before
// it spends four cross builds on it.
func TestABuildToldToSignWithAnIdentityItHasNotStops(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	distDir := t.TempDir()
	cmd := exec.Command("make", "dist-app",
		"VERSION="+releaseAppVersion, "DISTDIR="+distDir,
		"SIGN_IDENTITY=Developer ID Application: Nobody This Machine Has (ZZZZZZZZZZ)")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("make dist-app built a release with an identity that is not in the keychain:\n%s", out)
	}
	if !strings.Contains(string(out), "no codesigning identity matching") {
		t.Fatalf("make dist-app failed, but not by refusing the identity: %v\n%s", err, out)
	}
	zips, err := filepath.Glob(filepath.Join(distDir, "*.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if len(zips) != 0 {
		t.Fatalf("a build that could not sign still left %v behind", zips)
	}
}
