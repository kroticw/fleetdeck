//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// The sessions surface says in the window's log what grounds and corners the
// boxes its list lies in have (web/js/standreport.js, groundsReport), and the
// stand holds them to one ground: the glass, or the opaque panel's body
// (scripts/ci-window-stand.sh). A report of grounds is not one of scrolling.
func TestOnAStandTheSessionsSurfaceReportsItsGroundsToTheLog(t *testing.T) {
	var logged []string
	b := newBridge()
	handleStandReports(b, true, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	report := `{"surface":"sessions","report":"grounds","glass":"glass","elements":[{"selector":"body","background":"rgba(0, 0, 0, 0)","image":"none","radius":"0px","shadow":"none"}]}`
	if _, err := b.call("sessions", standReportBindingName, json.RawMessage(report)); err != nil {
		t.Fatal(err)
	}
	want := "fleetdeck-window: the sessions surface reports its grounds: " + report
	if len(logged) != 1 || logged[0] != want {
		t.Fatalf("logged %q, want %q", logged, want)
	}
}

// The stand reads the board's report with sed, as it reads the surfaces': the
// report goes to the log as the JSON the page sent, not as Go prints a map.
func TestTheBoardsReportGoesToTheLogAsTheJSONThePageSent(t *testing.T) {
	report := json.RawMessage(`{"overflowY":"auto","scrollbarWidth":15,"contentTallerThanRoom":true}`)
	want := `fleetdeck-window: the board reports its scrolling: {"overflowY":"auto","scrollbarWidth":15,"contentTallerThanRoom":true}`
	if got := boardStandReportLine(report); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
