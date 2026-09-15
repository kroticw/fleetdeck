package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// scripts/standcheck reads these words and these fields from the window's log;
// its own tests write lines with the same ones.
func TestAFrameReportLineIsWhatTheStandsCheckerReads(t *testing.T) {
	line := frameReportLine(standFrameReport{Glass: "glass", Capsules: []measuredCapsule{{Name: "tabs", measuredBox: measuredBox{X: 278, Y: 10, W: 123, H: 32}}}})
	const words = "fleetdeck-window: the frame measures "
	if !strings.HasPrefix(line, words) {
		t.Fatalf("line %q, want it to start %q", line, words)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, words)), &fields); err != nil {
		t.Fatal(err)
	}
	if got, want := keys(fields), []string{"capsules", "close", "fullScreen", "glass", "orchestrator", "row", "segmentBorderShape", "selectedTopInset", "sessions"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fields %v, want %v", got, want)
	}
	var capsules []map[string]json.RawMessage
	if err := json.Unmarshal(fields["capsules"], &capsules); err != nil {
		t.Fatal(err)
	}
	if got, want := keys(capsules[0]), []string{"h", "name", "w", "x", "y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a capsule's fields %v, want %v", got, want)
	}
}

func keys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Measured on the capsule probe's hidden window, glass, both panels unfolded:
// the row where the geometry put it, the tabs at its left edge, the panels at
// their widths, and the tabs a capsule.
func TestTheFrameMeasuresWhereTheGeometryPutItsPanelsAndCapsules(t *testing.T) {
	m := capsulesResult.measured
	g := layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})
	if m.Row != (measuredBox{X: g.Capsules.X, Y: g.Capsules.Y, W: g.Capsules.W, H: g.Capsules.H}) {
		t.Errorf("row measured %+v, want the geometry's %+v", m.Row, g.Capsules)
	}
	if m.Orchestrator.W != 368 || m.Sessions.W != 348 {
		t.Errorf("panels measured %v and %v wide, want 368 and 348", m.Orchestrator.W, m.Sessions.W)
	}
	if len(m.Capsules) == 0 || m.Capsules[0].Name != "tabs" || m.Capsules[0].X != g.Capsules.X {
		t.Errorf("capsules measured %+v, want the tabs first at the row's left edge %v", m.Capsules, g.Capsules.X)
	}
	if m.Glass != "glass" || m.FullScreen {
		t.Errorf("measured glass %q, full screen %v; want glass out of full screen", m.Glass, m.FullScreen)
	}
}
