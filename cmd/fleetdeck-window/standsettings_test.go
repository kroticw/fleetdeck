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
}

func TestAStandSetsTheDeadlineTheSizeAndTheAppearance(t *testing.T) {
	s, err := standSettingsFrom("/tmp/stand/no-daemon.sock", func(name string) (string, bool) {
		v, ok := standValues[name]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.startTimeout() != 10*time.Second || s.appearance != "dark" || s.open != "newcard" || !s.fullScreen {
		t.Fatalf("settings %+v", s)
	}
	if w, h := s.size(); w != 1000 || h != 700 {
		t.Fatalf("size %dx%d, want 1000x700", w, h)
	}
}

func TestAStandSettingThatMakesNoSenseIsRefusedByName(t *testing.T) {
	for name, value := range map[string]string{
		standPanelStartTimeoutEnv: "ten seconds",
		standWindowSizeEnv:        "300x200",
		standAppearanceEnv:        "auto",
		standOpenEnv:              "card",
		standFullScreenEnv:        "yes",
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
