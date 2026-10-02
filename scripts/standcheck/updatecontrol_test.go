package main

import (
	"strings"
	"testing"
)

// updateLine is the window's log line for the orchestrator's report of its
// update control, as the page sends it.
func updateLine(json string) string {
	return "2026/10/02 12:00:00 fleetdeck-window: the orchestrator surface reports its update control: " + json + "\n"
}

// A stand that opened the update control in a state of Check for Updates… has
// its frame of that state only if the header shows it: a screenshot of an
// empty header beside the brand reads as a check that said nothing.
func TestTheUpdateControlShowsWhatTheStandOpened(t *testing.T) {
	cases := []struct {
		open, report string
	}{
		{"check-checking", `{"report":"update","text":"Checking for updates…","button":false,"problem":false}`},
		{"check-latest", `{"report":"update","text":"Nothing newer: v1.0.0 is the latest","button":false,"problem":false}`},
		{"check-failed", `{"report":"update","text":"Could not check for updates: GitHub could not be reached (dial tcp: lookup github.com: no such host)","button":false,"problem":true}`},
		{"check-available", `{"report":"update","text":"v1.1.0 is available","button":true,"problem":false}`},
	}
	for _, c := range cases {
		t.Run(c.open, func(t *testing.T) {
			if problems := updateControlCheck(updateLine(c.report), []string{c.open}); len(problems) != 0 {
				t.Fatalf("a header showing what was opened failed: %q", problems)
			}
		})
	}
}

func TestAnUpdateControlNotShowingWhatTheStandOpenedFails(t *testing.T) {
	cases := []struct {
		name, open, log, want string
	}{
		{"no report", "check-latest", "", "did not report its update control"},
		{"empty", "check-checking", updateLine(`{"report":"update","text":"","button":false,"problem":false}`), "shows no words"},
		{"latest with no version", "check-latest", updateLine(`{"report":"update","text":"Nothing newer","button":false,"problem":false}`), "v1.0.0"},
		{"latest with a button", "check-latest", updateLine(`{"report":"update","text":"Nothing newer: v1.0.0 is the latest","button":true,"problem":false}`), "button"},
		{"failure not marked", "check-failed", updateLine(`{"report":"update","text":"Could not check for updates: x (no such host)","button":false,"problem":false}`), "problem"},
		{"failure without particulars", "check-failed", updateLine(`{"report":"update","text":"Could not check for updates","button":false,"problem":true}`), "no such host"},
		{"found with no button", "check-available", updateLine(`{"report":"update","text":"v1.1.0 is available","button":false,"problem":false}`), "button"},
		{"not a report", "check-available", updateLine(`{"report":`), "not one"},
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

// The last report is the frame's: the control is painted at load and then
// held, and an earlier, empty paint is not what the screenshot shows.
func TestTheLastUpdateReportIsTheOneHeldToTheFrame(t *testing.T) {
	log := updateLine(`{"report":"update","text":"","button":false,"problem":false}`) +
		updateLine(`{"report":"update","text":"v1.1.0 is available","button":true,"problem":false}`)
	if problems := updateControlCheck(log, []string{"check-available"}); len(problems) != 0 {
		t.Fatalf("problems = %q", problems)
	}
}

func TestAStandThatOpenedNoUpdateStateAsksNothingOfTheControl(t *testing.T) {
	if problems := updateControlCheck("", []string{"fleetmenu"}); len(problems) != 0 {
		t.Fatalf("problems = %q", problems)
	}
}
