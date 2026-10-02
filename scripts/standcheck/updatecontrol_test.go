package main

import (
	"strings"
	"testing"
)

// updateLine is the window's log line for the orchestrator's report of its
// update panel, as the page sends it.
func updateLine(json string) string {
	return "2026/10/02 12:00:00 fleetdeck-window: the orchestrator surface reports its update control: " + json + "\n"
}

// panelReport is a panel that shows text over the top of a 313 pt column's
// terminal, inset from its sides, with its words inside its box.
func panelReport(text string, button, problem bool) string {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	return `{"report":"update","text":"` + text + `","button":` + b(button) + `,"problem":` + b(problem) +
		`,"shown":true,"box":{"top":148,"left":8,"right":305,"bottom":212},"termTop":140,"pageWidth":313,"clipped":false}`
}

// A stand that opened the update control in a state of Check for Updates… has
// its frame of that state only if the panel shows it, where it belongs, whole.
func TestTheUpdatePanelShowsWhatTheStandOpened(t *testing.T) {
	cases := []struct {
		open, report string
	}{
		{"check-checking", panelReport("Checking for updates…", false, false)},
		{"check-latest", panelReport("Nothing newer: v1.0.0 is the latest", false, false)},
		{"check-failed", panelReport("Could not check for updates: GitHub could not be reached (dial tcp: lookup github.com: no such host)", false, true)},
		{"check-available", panelReport("v1.1.0 is available", true, false)},
	}
	for _, c := range cases {
		t.Run(c.open, func(t *testing.T) {
			if problems := updateControlCheck(updateLine(c.report), []string{c.open}); len(problems) != 0 {
				t.Fatalf("a panel showing what was opened failed: %q", problems)
			}
		})
	}
}

func TestAnUpdatePanelNotShowingWhatTheStandOpenedFails(t *testing.T) {
	latest := panelReport("Nothing newer: v1.0.0 is the latest", false, false)
	cases := []struct {
		name, open, log, want string
	}{
		{"no report", "check-latest", "", "did not report its update control"},
		{"empty", "check-checking", updateLine(panelReport("", false, false)), "shows no words"},
		{"latest with no version", "check-latest", updateLine(panelReport("Nothing newer", false, false)), "v1.0.0"},
		{"latest with a button", "check-latest", updateLine(panelReport("Nothing newer: v1.0.0 is the latest", true, false)), "button"},
		{"failure not marked", "check-failed", updateLine(panelReport("Could not check for updates: x (no such host)", false, false)), "problem"},
		{"failure without particulars", "check-failed", updateLine(panelReport("Could not check for updates", false, true)), "no such host"},
		{"found with no button", "check-available", updateLine(panelReport("v1.1.0 is available", false, false)), "button"},
		{"not a report", "check-available", updateLine(`{"report":`), "not one"},
		{"hidden", "check-latest", updateLine(strings.Replace(latest, `"shown":true`, `"shown":false`, 1)), "not shown"},
		{"words clipped", "check-latest", updateLine(strings.Replace(latest, `"clipped":false`, `"clipped":true`, 1)), "clipped"},
		{"over the island's head", "check-latest", updateLine(strings.Replace(latest, `"top":148`, `"top":120`, 1)), "above the terminal"},
		{"past the page's right edge", "check-latest", updateLine(strings.Replace(latest, `"right":305`, `"right":330`, 1)), "past the page"},
		{"past the page's left edge", "check-latest", updateLine(strings.Replace(latest, `"left":8`, `"left":-4`, 1)), "past the page"},
		{"no box", "check-latest", updateLine(strings.Replace(latest, `"box":{"top":148,"left":8,"right":305,"bottom":212}`, `"box":null`, 1)), "no box"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := updateControlCheck(c.log, []string{c.open})
			if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Fatalf("problems = %q, want one naming %q", problems, c.want)
			}
		})
	}
}

// The last report is the frame's: the panel is painted at load, placed once
// the terminal is there, and then held; an earlier paint is not what the
// screenshot shows.
func TestTheLastUpdateReportIsTheOneHeldToTheFrame(t *testing.T) {
	log := updateLine(strings.Replace(panelReport("v1.1.0 is available", true, false), `"shown":true`, `"shown":false`, 1)) +
		updateLine(panelReport("v1.1.0 is available", true, false))
	if problems := updateControlCheck(log, []string{"check-available"}); len(problems) != 0 {
		t.Fatalf("problems = %q", problems)
	}
}

func TestAStandThatOpenedNoUpdateStateAsksNothingOfTheControl(t *testing.T) {
	if problems := updateControlCheck("", []string{"fleetmenu"}); len(problems) != 0 {
		t.Fatalf("problems = %q", problems)
	}
}

// The panel is a panel of glass floating over the terminal, held to what a
// floating panel is held to, and reported whenever a check state is open.
func TestTheUpdatePanelIsHeldToTheMaterialOfAFloatingPanel(t *testing.T) {
	if !floatingPanels["updatePanel"] {
		t.Fatal("the update panel is not held to a floating panel's material")
	}
	for _, open := range []string{"check-checking", "check-latest", "check-failed", "check-available"} {
		o, ok := opened[open]
		if !ok || o.surface != "orchestrator" || len(o.names) != 1 || o.names[0] != "updatePanel" {
			t.Fatalf("%s does not require the orchestrator to report its update panel: %+v", open, o)
		}
	}
}
