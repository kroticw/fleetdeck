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
	got := strings.Join(panelArgs(4242, "", urlPort(t, "http://127.0.0.1:7778/"), "/h/.config/fleetdeck/dev/config.yaml"), " ")
	if want := "--owner-pid 4242 --port 7778 --config /h/.config/fleetdeck/dev/config.yaml"; got != want {
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

// The installed panel listens on server.port of the operator's config, and a
// dev app on that port would be looking at the installed panel.
func TestADevAppRefusesThePortOfTheInstalledPanel(t *testing.T) {
	dir := t.TempDir()
	operatorPath := filepath.Join(dir, "config.yaml")
	writeFile(t, operatorPath, operatorConfig)
	if err := devPortRefusal(7801, operatorPath); err == nil || !strings.Contains(err.Error(), "7801") {
		t.Fatalf("devPortRefusal on server.port = %v; want a refusal naming 7801", err)
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
	dev := devConfigPath(dir)
	// What an earlier dev app left there is replaced, not kept.
	writeFile(t, dev, "server:\n    port: 1\n")

	if err := copyDevConfig(operatorPath, dev, 7778); err != nil {
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
	dev := devConfigPath(dir)
	if err := copyDevConfig(filepath.Join(dir, "none.yaml"), dev, 7778); err == nil {
		t.Fatal("copyDevConfig with no operator's config: want a refusal")
	}
	if _, err := os.Stat(dev); err == nil {
		t.Fatal("a dev config was written with no operator's config to copy")
	}
}

func TestADevAppsConfigIsBesideTheOperatorsInADirectoryOfItsOwn(t *testing.T) {
	if got, want := devConfigPath("/h"), "/h/.config/fleetdeck/dev/config.yaml"; got != want {
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

func TestADevAppKeepsItsOwnLogWidthsAndLock(t *testing.T) {
	if got := panelLogPath("/h", false); got != "/h/Library/Logs/fleetdeck.log" {
		t.Errorf("the app's log = %q", got)
	}
	if got := panelLogPath("/h", true); got != "/h/Library/Logs/fleetdeck-dev.log" {
		t.Errorf("a dev app's log = %q", got)
	}
	if got := widthsSuite("", false); got != "" {
		t.Errorf("the app's widths go to suite %q, want its own defaults", got)
	}
	if got := widthsSuite("", true); got != supervisor.DevBundleID+".widths" {
		t.Errorf("a dev app's widths go to suite %q", got)
	}
	if dev, app := updateLockPath("/src/fleetdeck/bin/"+devBundleName), updateLockPath("/Applications/fleetdeck.app"); dev == app || dev == "" {
		t.Errorf("a dev app's lock %q is the installed app's %q", dev, app)
	}
}

func TestADevAppSaysItIsDev(t *testing.T) {
	if got := windowTitle(false); got != "fleetdeck" {
		t.Errorf("the app's title = %q", got)
	}
	if got := windowTitle(true); got != "fleetdeck dev" {
		t.Errorf("a dev app's title = %q", got)
	}
}

func TestADevAppsKeeperStopsOnlyItsOwnPanel(t *testing.T) {
	const exe = "/src/fleetdeck/bin/" + devBundleName + "/Contents/MacOS/fleetdeck-window"
	if got := stopsOnly(exe, true); got != panelBinary(exe) {
		t.Errorf("a dev app's keeper stops only %q, want its own panel %q", got, panelBinary(exe))
	}
	if got := stopsOnly(exe, false); got != "" {
		t.Errorf("the app's keeper stops only %q, want no such limit", got)
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
