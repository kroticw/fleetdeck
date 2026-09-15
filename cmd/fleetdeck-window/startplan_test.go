//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/kroticw/fleetdeck/internal/config"
)

// planStart is what the window works out before anything of it is made: the
// keeper of its panel, its logs, its panel widths, its title, how it updates,
// and whether it removes a leftover bundle. main takes each of them from the
// plan as it is, so each is checked here as the window gets it, for the
// installed app and for a dev app.

const installedExe = "/Applications/fleetdeck.app/Contents/MacOS/fleetdeck-window"

// devExe is the window of a dev app built into a tree of its own.
func devExe(t *testing.T) string {
	return filepath.Join(t.TempDir(), "bin", devBundleName, "Contents", "MacOS", "fleetdeck-window")
}

// checkStrings reports every field whose value is not the one wanted.
func checkStrings(t *testing.T, fields map[string][2]string) {
	t.Helper()
	for name, f := range fields {
		if f[0] != f[1] {
			t.Errorf("%s = %q, want %q", name, f[0], f[1])
		}
	}
}

func TestTheInstalledAppsStartIsTheAppsOwn(t *testing.T) {
	home := t.TempDir()
	plan, err := planStart(startInput{
		url:            defaultURL,
		exe:            installedExe,
		home:           home,
		pid:            4242,
		operatorConfig: filepath.Join(home, "none.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	k := plan.keeper
	checkStrings(t, map[string][2]string{
		"keeper args":  {strings.Join(k.Args, " "), "--owner-pid 4242 --port 7777"},
		"keeper URL":   {k.URL, defaultURL},
		"panel binary": {k.Bin, panelBinary(installedExe)},
		"stops only":   {k.StopsOnly, ""},
		"panel log":    {k.LogPath, filepath.Join(home, "Library", "Logs", "fleetdeck.log")},
		"window log":   {plan.windowLog, ""},
		"widths suite": {plan.widthsSuite, ""},
		"title":        {plan.title, "fleetdeck"},
	})
	if k.Owner != 4242 {
		t.Errorf("keeper owner = %d, want 4242", k.Owner)
	}
	if plan.way.dev {
		t.Error("the installed app works out its update as a dev app")
	}
	if !plan.retiresLeftover {
		t.Error("the installed app's start removes no leftover bundle")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "fleetdeck", "dev")); err == nil {
		t.Error("the installed app's start wrote a dev config")
	}
}

func TestADevAppsStartKeepsOffTheInstalledApp(t *testing.T) {
	home := t.TempDir()
	operatorPath := filepath.Join(home, "config.yaml")
	writeFile(t, operatorPath, operatorConfig)
	exe := devExe(t)
	plan, err := planStart(startInput{
		dev:            true,
		url:            "http://127.0.0.1:7778/",
		exe:            exe,
		home:           home,
		pid:            4242,
		operatorConfig: operatorPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	k := plan.keeper
	devConfig := devConfigPath(home, 7778)
	checkStrings(t, map[string][2]string{
		"keeper args":  {strings.Join(k.Args, " "), "--owner-pid 4242 --port 7778 --config " + devConfig},
		"stops only":   {k.StopsOnly, panelBinary(exe)},
		"panel log":    {k.LogPath, filepath.Join(home, "Library", "Logs", "fleetdeck-dev.log")},
		"window log":   {plan.windowLog, filepath.Join(home, "Library", "Logs", "fleetdeck-dev-window.log")},
		"widths suite": {plan.widthsSuite, devWidthsSuite},
		"title":        {plan.title, "fleetdeck dev"},
	})
	if !plan.way.dev {
		t.Error("a dev app works out its update as the installed app")
	}
	if plan.retiresLeftover {
		t.Error("a dev app's start removes a leftover bundle")
	}
	cfg, err := appconfig.Load(devConfig)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerPort != 7778 || cfg.Notify.Waiting {
		t.Errorf("the dev config copy has port %d and waiting banners %v; want 7778 and off", cfg.ServerPort, cfg.Notify.Waiting)
	}
	if data, _ := os.ReadFile(operatorPath); string(data) != operatorConfig {
		t.Errorf("the operator's config was changed:\n%s", data)
	}
}

// A refused dev app says why in its own log, which it knows before it refuses:
// opened from Finder its output goes nowhere else. It makes no keeper and
// writes no config copy.
func TestADevAppRefusesToStartAndStillNamesItsLog(t *testing.T) {
	home := t.TempDir()
	operatorPath := filepath.Join(home, "config.yaml")
	writeFile(t, operatorPath, operatorConfig) // server.port 7801
	for name, in := range map[string]startInput{
		"on the port the installed app hands its panel": {url: "http://127.0.0.1:7777/"},
		"on server.port":                    {url: "http://127.0.0.1:7801/"},
		"started as an update's new window": {url: "http://127.0.0.1:7778/", handover: "/Applications/.fleetdeck-update/handover"},
		"told the installed bundle":         {url: "http://127.0.0.1:7778/", canonical: "/Applications/fleetdeck.app"},
	} {
		in.dev, in.exe, in.home, in.pid, in.operatorConfig = true, devExe(t), home, 4242, operatorPath
		plan, err := planStart(in)
		if err == nil {
			t.Errorf("%s: the dev app started", name)
			continue
		}
		if want := filepath.Join(home, "Library", "Logs", "fleetdeck-dev-window.log"); plan.windowLog != want {
			t.Errorf("%s: window log = %q, want %q", name, plan.windowLog, want)
		}
		if plan.keeper != nil {
			t.Errorf("%s: a keeper was made for a refused start", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "fleetdeck", "dev")); err == nil {
		t.Error("a refused dev app wrote a config copy")
	}
}
