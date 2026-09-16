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

// T-070: the orchestrator surface and the board say how their capsules read in
// the window's material (web/js/standcontrols.js), each in a line of its own that
// scripts/standcheck reads; it is not a report of scrolling.
func TestTheCapsulesReportGoesToTheLogAsALineOfItsOwn(t *testing.T) {
	report := `{"surface":"orchestrator","report":"controls","glass":"glass","controls":[{"name":"picker","height":24,"radius":999,"fillAlpha":0.09,"backdrop":"none","floating":false,"contrast":9.1,"disabled":false}]}`
	var logged []string
	b := newBridge()
	handleStandReports(b, true, func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) })
	if _, err := b.call("orchestrator", standReportBindingName, json.RawMessage(report)); err != nil {
		t.Fatal(err)
	}
	if want := "fleetdeck-window: the orchestrator surface reports its controls: " + report; len(logged) != 1 || logged[0] != want {
		t.Fatalf("logged %q, want %q", logged, want)
	}
	if got, want := boardStandReportLine(json.RawMessage(report)), "fleetdeck-window: the board reports its controls: "+report; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// The board's word on a scrolled column after the panel's next snapshots is a
// line of its own: the stand's gate on the board's scrolling reads the last
// scrolling line, and this is not one.
func TestTheBoardsColumnScrollGoesToTheLogAsALineOfItsOwn(t *testing.T) {
	report := json.RawMessage(`{"report":"columnScroll","stage":"done","asked":120,"renders":2,"scrollTop":120}`)
	want := `fleetdeck-window: the board reports its column scroll: {"report":"columnScroll","stage":"done","asked":120,"renders":2,"scrollTop":120}`
	if got := boardStandReportLine(report); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// T-079: what stands in the way of the band the window is dragged by, with a
// sheet open, is a line of its own -- the stand's gate on the band reads it, and
// it is not a report of scrolling.
func TestTheBoardsTopBandGoesToTheLogAsALineOfItsOwn(t *testing.T) {
	report := json.RawMessage(`{"report":"topband","width":1000,"max":64,"height":60,"sheetOpen":["session-panel"],"rows":[{"y":60,"x":396,"tag":"DIV","id":"session-panel","class":"session-panel"}]}`)
	want := `fleetdeck-window: the board reports its top band: ` + string(report)
	if got := boardStandReportLine(report); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
