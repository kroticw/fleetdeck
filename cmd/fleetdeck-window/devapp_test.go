//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appconfig "github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// A dev app is this window built from a working tree by `make dev-app` and
// opened beside the installed app, to try a change to the window before it is
// released. It uses the operator's fleet daemon and board, and nothing else of
// the installed app's: not its port, its panel, its updates, its LaunchServices
// record, its log, its panel widths or its config file.

// urlPort is the port of url, which a test knows to have one.
func urlPort(t *testing.T, url string) int {
	t.Helper()
	port, err := panelPort(url)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// The installed app is started with no -url: its panel is on 7777, as it
// always was, now told so rather than left to read it from server.port.
func TestTheInstalledAppStartsItsPanelOnThePortOfItsDefaultURL(t *testing.T) {
	port := urlPort(t, defaultURL)
	if port != 7777 {
		t.Fatalf("panelPort(%q) = %d, want 7777", defaultURL, port)
	}
	if got := strings.Join(panelArgs(4242, "", port, ""), " "); got != "--owner-pid 4242 --port 7777" {
		t.Fatalf("panelArgs for the installed app = %q", got)
	}
}

// A window opened on another URL starts its panel on that URL's port: before,
// the panel took server.port whatever the window looked at.
func TestThePortOfTheWindowsURLReachesItsPanel(t *testing.T) {
	got := strings.Join(panelArgs(4242, "", urlPort(t, "http://127.0.0.1:7778/"), "/h/.config/fleetdeck/dev/7778/config.yaml"), " ")
	if want := "--owner-pid 4242 --port 7778 --config /h/.config/fleetdeck/dev/7778/config.yaml"; got != want {
		t.Fatalf("panelArgs = %q, want %q", got, want)
	}
}

// A URL that names no port says nothing of where the panel is to listen, and
// the window refuses it rather than guess.
func TestAURLWithNoPortIsRefused(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1/", "http://127.0.0.1:port/", "://"} {
		if port, err := panelPort(url); err == nil {
			t.Errorf("panelPort(%q) = %d; want a refusal", url, port)
		}
	}
}

// A dev app never updates, so nothing starts it to take a panel over: a
// handover names the installed bundle, and the takeover would stop the panel
// on the port and have LaunchServices register that bundle.
func TestADevAppRefusesToBeStartedAsAnUpdate(t *testing.T) {
	if err := devFlagsRefusal("", ""); err != nil {
		t.Fatalf("a dev app started by a person is refused: %v", err)
	}
	for _, c := range []struct{ handover, canonical, flag string }{
		{handover: "/Applications/.fleetdeck-update/handover", flag: "--handover"},
		{canonical: "/Applications/fleetdeck.app", flag: "--canonical"},
	} {
		err := devFlagsRefusal(c.handover, c.canonical)
		if err == nil || !strings.Contains(err.Error(), c.flag) {
			t.Errorf("devFlagsRefusal(%q, %q) = %v; want a refusal naming %s", c.handover, c.canonical, err, c.flag)
		}
	}
}

// operatorConfig is a configuration file as the operator's looks.
const operatorConfig = `board:
    path: /Users/me/obsidian/board
docs:
    paths:
        - /Users/me/obsidian/board/docs
orchestrator:
    session: 06a1f607
session_labels:
    06a1f607-1a29-4fb4-a02c-f1e7c5cf59a8: оркестр
notify:
    enabled:
        waiting: true
        failed: true
        silent: true
        card_blocked: true
    silence_after: 30m0s
daemon:
    poll_interval: 2s
usage:
    enabled: true
server:
    port: 7801
statusline:
    wrap: ""
    rate_limits_path: ""
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The installed app hands its panel the port of its default URL, 7777, and a
// panel started some other way listens on server.port of the operator's
// config: a dev app on either would be looking at the installed panel, which
// writes the operator's real config.
func TestADevAppRefusesThePortOfTheInstalledPanel(t *testing.T) {
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "config.yaml")
	writeFile(t, operatorPath, operatorConfig) // server.port 7801
	if err := devPortRefusal(7801, operatorPath); err == nil || !strings.Contains(err.Error(), "7801") {
		t.Fatalf("devPortRefusal on server.port = %v; want a refusal naming 7801", err)
	}
	if err := devPortRefusal(7777, operatorPath); err == nil || !strings.Contains(err.Error(), "7777") {
		t.Fatalf("devPortRefusal on the installed app's port, with server.port 7801 = %v; want a refusal naming 7777", err)
	}
	if err := devPortRefusal(7802, operatorPath); err != nil {
		t.Fatalf("devPortRefusal on another port = %v", err)
	}

	// With no config the installed panel is on the default port.
	missing := filepath.Join(dir, "none.yaml")
	if err := devPortRefusal(7777, missing); err == nil {
		t.Fatal("devPortRefusal(7777) with no config: want a refusal")
	}
	if err := devPortRefusal(7778, missing); err != nil {
		t.Fatalf("devPortRefusal(7778) with no config = %v", err)
	}
}

// The dev panel writes to a copy of the operator's config, made again at each
// start, with every banner off -- the installed panel already sends them --
// and with the dev app's own port, so a dev panel started without --port
// still keeps off the installed panel's.
func TestADevAppsConfigIsACopyOfTheOperatorsWithNoBannersOnItsOwnPort(t *testing.T) {
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "config.yaml")
	writeFile(t, operatorPath, operatorConfig)
	dev := devConfigPath(dir, 7778)
	// What an earlier dev app left there is replaced, not kept.
	writeFile(t, dev, "server:\n    port: 1\n")

	if err := copyDevConfig(operatorPath, dev, "", 7778); err != nil {
		t.Fatal(err)
	}
	want, err := appconfig.Load(operatorPath)
	if err != nil {
		t.Fatal(err)
	}
	want.Notify.Waiting, want.Notify.Failed, want.Notify.Silent, want.Notify.CardBlocked = false, false, false, false
	want.ServerPort = 7778
	got, err := appconfig.Load(dev)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the dev config is\n%+v\nwant the operator's with banners off\n%+v", got, want)
	}
	if data, _ := os.ReadFile(operatorPath); string(data) != operatorConfig {
		t.Fatalf("the operator's config was changed:\n%s", data)
	}
}

// With no operator's config there is nothing to copy, and a dev panel with no
// config would start in setup, writing the operator's Claude Code settings.
func TestADevAppWithNoOperatorConfigRefuses(t *testing.T) {
	dir := t.TempDir()
	dev := devConfigPath(dir, 7778)
	if err := copyDevConfig(filepath.Join(dir, "none.yaml"), dev, "", 7778); err == nil {
		t.Fatal("copyDevConfig with no operator's config: want a refusal")
	}
	if _, err := os.Stat(dev); err == nil {
		t.Fatal("a dev config was written with no operator's config to copy")
	}
}

// Dev apps from two trees run at once on two ports, and one's start must not
// write over the copy the other's panel is running on.
func TestTwoDevAppsOnTwoPortsKeepTwoConfigCopies(t *testing.T) {
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "config.yaml")
	writeFile(t, operatorPath, operatorConfig)
	first, second := devConfigPath(dir, 7778), devConfigPath(dir, 7779)
	if first == second {
		t.Fatalf("two dev ports share the config copy %s", first)
	}
	if err := copyDevConfig(operatorPath, first, "", 7778); err != nil {
		t.Fatal(err)
	}
	if err := copyDevConfig(operatorPath, second, "", 7779); err != nil {
		t.Fatal(err)
	}
	for path, port := range map[string]int{first: 7778, second: 7779} {
		cfg, err := appconfig.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ServerPort != port {
			t.Errorf("%s has server.port %d, want %d", path, cfg.ServerPort, port)
		}
	}
}

// fleetsNamed is every fleet called name in the config at path.
func fleetsNamed(t *testing.T, path, name string) []fleet.Fleet {
	t.Helper()
	cfg, err := appconfig.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var named []fleet.Fleet
	for _, f := range cfg.FleetList() {
		if f.Name == name {
			named = append(named, f)
		}
	}
	return named
}

// A fleet made from a dev app's panel is written to its config copy, and the
// panel asks to be restarted to work in it: the copy that start makes keeps it.
func TestAFleetMadeInADevAppIsKeptAtItsNextStart(t *testing.T) {
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "config.yaml")
	writeFile(t, operatorPath, operatorConfig)
	dev := devConfigPath(dir, 7778)
	if err := copyDevConfig(operatorPath, dev, "", 7778); err != nil {
		t.Fatal(err)
	}
	board := t.TempDir()
	if err := appconfig.AddFleet(dev, fleet.Fleet{Name: "testdeck", BoardPath: board}); err != nil {
		t.Fatal(err)
	}

	if err := copyDevConfig(operatorPath, dev, "", 7778); err != nil {
		t.Fatal(err)
	}
	if kept := fleetsNamed(t, dev, "testdeck"); len(kept) != 1 || kept[0].BoardPath != board {
		t.Fatalf("after the next start the dev copy has fleets named testdeck %+v; want the one made in it, on %s", kept, board)
	}
	if data, _ := os.ReadFile(operatorPath); string(data) != operatorConfig {
		t.Fatalf("the operator's config was changed:\n%s", data)
	}
}

// A fleet the operator's config has is the operator's, whatever a dev panel
// made under the same name.
func TestTheOperatorsFleetWinsOverADevFleetOfTheSameName(t *testing.T) {
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "config.yaml")
	writeFile(t, operatorPath, operatorConfig)
	dev := devConfigPath(dir, 7778)
	if err := copyDevConfig(operatorPath, dev, "", 7778); err != nil {
		t.Fatal(err)
	}
	devBoard, operatorBoard := t.TempDir(), t.TempDir()
	if err := appconfig.AddFleet(dev, fleet.Fleet{Name: "work", BoardPath: devBoard}); err != nil {
		t.Fatal(err)
	}
	if err := appconfig.AddFleet(operatorPath, fleet.Fleet{Name: "work", BoardPath: operatorBoard}); err != nil {
		t.Fatal(err)
	}

	if err := copyDevConfig(operatorPath, dev, "", 7778); err != nil {
		t.Fatal(err)
	}
	if kept := fleetsNamed(t, dev, "work"); len(kept) != 1 || kept[0].BoardPath != operatorBoard {
		t.Fatalf("the dev copy has fleets named work %+v; want only the operator's, on %s", kept, operatorBoard)
	}
}

// The copies were one shared file before they were kept per port. A dev app's
// first start on a port with no copy of its own keeps the fleets made in the
// shared one, and leaves that file as it was.
func TestADevAppKeepsTheFleetsOfTheSharedCopyOnItsFirstStartOnAPort(t *testing.T) {
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "config.yaml")
	writeFile(t, operatorPath, operatorConfig)
	legacy := legacyDevConfigPath(dir)
	if err := copyDevConfig(operatorPath, legacy, "", 7778); err != nil {
		t.Fatal(err)
	}
	board := t.TempDir()
	if err := appconfig.AddFleet(legacy, fleet.Fleet{Name: "testdeck", BoardPath: board}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}

	dev := devConfigPath(dir, 7778)
	if err := copyDevConfig(operatorPath, dev, legacy, 7778); err != nil {
		t.Fatal(err)
	}
	if kept := fleetsNamed(t, dev, "testdeck"); len(kept) != 1 || kept[0].BoardPath != board {
		t.Fatalf("the port's first copy has fleets named testdeck %+v; want the one made in the shared copy", kept)
	}
	if after, _ := os.ReadFile(legacy); string(after) != string(before) {
		t.Fatalf("the shared copy was changed:\n%s", after)
	}
}

func TestADevAppsConfigIsBesideTheOperatorsInADirectoryOfItsOwn(t *testing.T) {
	if got, want := devConfigPath("/h", 7778), "/h/.config/fleetdeck/dev/7778/config.yaml"; got != want {
		t.Fatalf("devConfigPath = %q, want %q", got, want)
	}
}

// Whatever it is built from, a dev app looks for no newer version and has no
// update to run: it would write last-update-check, which the installed app
// reads, and bring its tree forward or replace its bundle.
func TestADevAppNeverUpdates(t *testing.T) {
	for _, cfg := range []config{
		{tree: "/src/fleetdeck", exe: inBundle, version: "dev"},
		{exe: inBundle, version: "v0.10.1", teamID: "PTLLPQ8LY4"},
		{exe: inBundle, version: "dev"},
	} {
		cfg.dev = true
		if how := updateWay(cfg); how.Refusal != refusalDev || how.Source != nil {
			t.Errorf("updateWay(%+v) = %+v; want the %q refusal and no source", cfg, how, refusalDev)
		}
	}
}

func TestADevAppRemovesNoLeftoverBundle(t *testing.T) {
	const bundle = "/src/fleetdeck/bin/" + devBundleName
	if retiresLeftover("", bundle, true) {
		t.Fatal("a dev app's start removes a leftover bundle")
	}
	if !retiresLeftover("", bundle, false) {
		t.Fatal("the same start of an app that is not a dev app removes none")
	}
}

// Opened from Finder, with no -url, a dev app is on its own port still; the
// installed app ignores a dev URL even if one were written into it.
func TestADevAppOpensOnItsOwnPortWithNoURL(t *testing.T) {
	const dev = "http://127.0.0.1:7778/"
	if got := startURL(true, dev); got != dev {
		t.Errorf("a dev app starts on %q, want %q", got, dev)
	}
	if got := startURL(false, dev); got != defaultURL {
		t.Errorf("the app starts on %q, want %q", got, defaultURL)
	}
	if got := startURL(true, ""); got != defaultURL {
		t.Errorf("a dev app with no dev URL starts on %q, want %q", got, defaultURL)
	}
}

// envWithout is the environment with name taken out.
func envWithout(name string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, name+"=") {
			env = append(env, kv)
		}
	}
	return env
}

// `make dev-app` builds on this machine as it is: with no SDKROOT, which a
// Command Line Tools SDK newer than the selected linker understands breaks
// (docs/engineering/dev-app.md), and without opening the window on the screen
// of whoever runs the tests.
func TestMakeDevAppBuildsTheDevBundleWithoutOpeningIt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the whole app bundle")
	}
	bindir := t.TempDir()
	cmd := exec.Command("make", "dev-app", "BINDIR="+bindir, "DEV_OPEN=0", "DEV_PORT=7791")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = envWithout("SDKROOT")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make dev-app: %v\n%s", err, out)
	}
	app := filepath.Join(bindir, devBundleName)
	forgetBundle(t, app)

	sdk, err := exec.Command("xcrun", "--sdk", "macosx", "--show-sdk-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	if line := "dev-app: SDK " + strings.TrimSpace(string(sdk)); !strings.Contains(string(out), line) {
		t.Errorf("make dev-app does not say %q:\n%s", line, out)
	}
	if !strings.Contains(string(out), "not opened (DEV_OPEN=0)") {
		t.Errorf("make dev-app does not say it left the app unopened:\n%s", out)
	}

	keys := plistKeys(t, filepath.Join(app, "Contents", "Info.plist"))
	for key, want := range map[string]string{
		"CFBundleIdentifier":  supervisor.DevBundleID,
		"CFBundleName":        "fleetdeck dev",
		"CFBundleDisplayName": "fleetdeck dev",
	} {
		if keys[key] != want {
			t.Errorf("%s = %v, want %q", key, keys[key], want)
		}
	}

	window := filepath.Join(app, "Contents", "MacOS", "fleetdeck-window")
	if _, err := os.Stat(panelBinary(window)); err != nil {
		t.Fatalf("the dev bundle lacks its panel: %v", err)
	}
	info, err := exec.Command("go", "version", "-m", window).CombinedOutput()
	if err != nil {
		t.Fatalf("go version -m: %v\n%s", err, info)
	}
	for _, want := range []string{"main.devBuild=true", "main.devURL=http://127.0.0.1:7791/"} {
		if !strings.Contains(string(info), want) {
			t.Errorf("the dev window was not built with %s; its build record:\n%s", want, info)
		}
	}
	if strings.Contains(string(info), "main.treeDir") {
		t.Errorf("the dev window carries a source tree to update from; its build record:\n%s", info)
	}
}

// Which SDK a cgo build links against is scripts/darwin-sdkroot.sh's answer:
// SDKROOT when it is set, and otherwise what xcrun says the selected developer
// directory's SDK is. The script is run here with an xcrun of the test's own
// first on PATH and no /usr/bin on it at all, so neither make nor any xcrun
// shim runs under the made-up paths below.
func TestTheSDKIsTheOneGivenOrTheSelectedDeveloperDirectorys(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "darwin-sdkroot.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fakeXcrun := func(answer string) (dir, calls string) {
		dir = t.TempDir()
		calls = filepath.Join(dir, "calls")
		body := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n"
		if answer != "" {
			body += "echo '" + answer + "'\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "xcrun"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir, calls
	}
	run := func(dir, sdkroot string) (string, string, error) {
		cmd := exec.Command("/bin/sh", script)
		cmd.Env = []string{"PATH=" + dir + ":/bin", "SDKROOT=" + sdkroot}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), stderr.String(), err
	}

	dir, calls := fakeXcrun("/Selected/MacOSX.sdk")
	if got, _, err := run(dir, "/Given/MacOSX.sdk"); err != nil || got != "/Given/MacOSX.sdk" {
		t.Errorf("with SDKROOT given: %q, %v; want the given SDK", got, err)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Error("with SDKROOT given, xcrun was asked anyway")
	}
	if got, _, err := run(dir, ""); err != nil || got != "/Selected/MacOSX.sdk" {
		t.Errorf("with no SDKROOT: %q, %v; want xcrun's answer", got, err)
	}
	if data, _ := os.ReadFile(calls); strings.TrimSpace(string(data)) != "--sdk macosx --show-sdk-path" {
		t.Errorf("xcrun was asked %q, want the selected developer directory's SDK", data)
	}

	silent, _ := fakeXcrun("")
	if got, stderr, err := run(silent, ""); err == nil || got != "" || !strings.Contains(stderr, "SDKROOT") {
		t.Errorf("with xcrun answering nothing: %q, %v, %q; want a failure that says to set SDKROOT", got, err, stderr)
	}

	// An Xcode whose license is not accepted: xcrun exits 69 and says why,
	// and the build says it too, rather than building against no SDK.
	unlicensed := t.TempDir()
	refusal := "#!/bin/sh\necho 'You have not agreed to the Xcode license agreements.' >&2\nexit 69\n"
	if err := os.WriteFile(filepath.Join(unlicensed, "xcrun"), []byte(refusal), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, stderr, err := run(unlicensed, ""); err == nil || got != "" || !strings.Contains(stderr, "exit 69") || !strings.Contains(stderr, "license") {
		t.Errorf("with xcrun refusing for its license: %q, %v, %q; want a failure naming exit 69 and what xcrun said", got, err, stderr)
	}
}

// An SDKROOT in the environment is used as it is. It is given a real SDK, one
// beside the SDK make would choose: /usr/bin/make is xcrun's shim, which does
// not run make at all under an SDKROOT that is no SDK, and asks macOS to
// install the Command Line Tools while it is at it -- a dialog on the screen of
// whoever runs the tests.
func TestMakeKeepsAnSDKROOTItIsGiven(t *testing.T) {
	chosen, err := exec.Command("xcrun", "--sdk", "macosx", "--show-sdk-path").Output()
	if err != nil {
		t.Fatal(err)
	}
	sdk := strings.TrimSpace(string(chosen))
	beside, err := filepath.Glob(filepath.Join(filepath.Dir(sdk), "MacOSX*.sdk"))
	if err != nil {
		t.Fatal(err)
	}
	given := ""
	for _, other := range beside {
		if other != sdk {
			given = other
			break
		}
	}
	if given == "" {
		t.Skipf("no other macOS SDK beside %s to give make", sdk)
	}

	cmd := exec.Command("make", "--dry-run", "dev-app", "DEV_OPEN=0")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(envWithout("SDKROOT"), "SDKROOT="+given)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make --dry-run dev-app: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `SDKROOT="`+given+`"`) {
		t.Fatalf("make dev-app does not build with the SDKROOT it was given, %s:\n%s", given, out)
	}
}
