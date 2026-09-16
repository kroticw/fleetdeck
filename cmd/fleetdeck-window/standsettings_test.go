//go:build darwin

package main

import (
	"strings"
	"testing"
	"time"
)

var standValues = map[string]string{
	standPanelStartTimeoutEnv: "10s",
	standWindowSizeEnv:        "1000x700",
	standAppearanceEnv:        "dark",
	standOpenEnv:              "newcard",
	standFullScreenEnv:        "on",
	standFoldEnv:              "both",
}

// A person's window: every stand variable set, and none of it read, because
// the stand's socket is not.
func TestAWindowOffAStandReadsNoStandSetting(t *testing.T) {
	var asked []string
	lookup := func(name string) (string, bool) {
		asked = append(asked, name)
		v, ok := standValues[name]
		return v, ok
	}
	s, err := standSettingsFrom("", lookup)
	if err != nil || s != (standSettings{}) || len(asked) != 0 {
		t.Fatalf("off a stand: settings %+v, err %v, variables read %v; want none of them", s, err, asked)
	}
	if s.startTimeout() != panelStartTimeout {
		t.Fatalf("off a stand the panel has %v to answer, want %v", s.startTimeout(), panelStartTimeout)
	}
	if w, h := s.size(); w != 1440 || h != 900 {
		t.Fatalf("off a stand the window is %dx%d, want 1440x900", w, h)
	}
	loaded := panelWidths{Orchestrator: 300, Sessions: 260, SessionsFolded: true}
	if got := panelWidthsForStand(loaded, s.fold); got != loaded || !storesWidths(s.fold) {
		t.Fatalf("off a stand the panels open %+v, widths stored %v; want %+v as the defaults say, and stored", got, storesWidths(s.fold), loaded)
	}
}

func TestAStandToldHowToFoldOpensSoAndKeepsNoWidths(t *testing.T) {
	loaded := panelWidths{Orchestrator: 300, Sessions: 260, SessionsFolded: true}
	for fold, want := range map[string]panelWidths{
		"orchestrator": {Orchestrator: 300, Sessions: 260, OrchestratorFolded: true},
		"sessions":     {Orchestrator: 300, Sessions: 260, SessionsFolded: true},
		"both":         {Orchestrator: 300, Sessions: 260, OrchestratorFolded: true, SessionsFolded: true},
	} {
		if got := panelWidthsForStand(loaded, fold); got != want {
			t.Errorf("fold %q: the panels open %+v, want %+v", fold, got, want)
		}
		if storesWidths(fold) {
			t.Errorf("fold %q: the window stores its widths and folds", fold)
		}
	}
}

func TestAStandSetsTheDeadlineTheSizeAndTheAppearance(t *testing.T) {
	s, err := standSettingsFrom("/tmp/stand/no-daemon.sock", func(name string) (string, bool) {
		v, ok := standValues[name]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.startTimeout() != 10*time.Second || s.appearance != "dark" || s.open != "newcard" || !s.fullScreen || s.fold != "both" {
		t.Fatalf("settings %+v", s)
	}
	if w, h := s.size(); w != 1000 || h != 700 {
		t.Fatalf("size %dx%d, want 1000x700", w, h)
	}
}

// T-070: a stand's frame shows the new card form and the fleet menu's list
// open at once, each on its own surface.
func TestAStandOpensTheNewCardFormTheFleetMenuOrBoth(t *testing.T) {
	for _, value := range []string{"newcard", "fleetmenu", "session", "newcard,fleetmenu", "newcard,session"} {
		s, err := standSettingsFrom("/tmp/stand/no-daemon.sock", func(n string) (string, bool) {
			if n == standOpenEnv {
				return value, true
			}
			return "", false
		})
		if err != nil || s.open != value {
			t.Errorf("%s=%q: open %q, err %v; want it opened as said", standOpenEnv, value, s.open, err)
		}
	}
}

func TestAStandSettingThatMakesNoSenseIsRefusedByName(t *testing.T) {
	for _, bad := range []string{"card", "newcard,card", "newcard,", ""} {
		_, err := standSettingsFrom("/tmp/stand/no-daemon.sock", func(n string) (string, bool) {
			if n == standOpenEnv {
				return bad, true
			}
			return "", false
		})
		if err == nil || !strings.Contains(err.Error(), standOpenEnv) {
			t.Errorf("%s=%q: err %v, want a refusal naming the variable", standOpenEnv, bad, err)
		}
	}
	for name, value := range map[string]string{
		standPanelStartTimeoutEnv: "ten seconds",
		standWindowSizeEnv:        "300x200",
		standAppearanceEnv:        "auto",
		standOpenEnv:              "card",
		standFullScreenEnv:        "yes",
		standFoldEnv:              "left",
	} {
		_, err := standSettingsFrom("/tmp/stand/no-daemon.sock", func(n string) (string, bool) {
			if n == name {
				return value, true
			}
			return "", false
		})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%q: err %v, want a refusal naming the variable", name, value, err)
		}
	}
}
