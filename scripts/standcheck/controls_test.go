package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A capsule as the page reports it in the window's material: a see-through fill
// on glass, a solid one with no glass, round, not blurred, its text well read.
func goodCapsule(name, glass string) control {
	c := control{Name: name, Height: 24, Radius: 999, FillAlpha: 0.09, Backdrop: "none", Contrast: 9.2}
	if glass == "opaque" {
		c.FillAlpha = 1
	}
	return c
}

// A panel floating over content: frosted on glass, solid with no glass.
func goodPanel(name, glass string) control {
	c := control{Name: name, Height: 90, Radius: 18, FillAlpha: 0.8, Backdrop: "blur(24px) saturate(160%)", Contrast: 12.4}
	if glass == "opaque" {
		c.FillAlpha, c.Backdrop = 1, "none"
	}
	return c
}

func goodOrchestratorControls(glass string) controlsReport {
	return controlsReport{Surface: "orchestrator", Report: "controls", Glass: glass, Controls: []control{
		goodCapsule("editPencil", glass), goodCapsule("picker", glass), goodCapsule("fontSmaller", glass),
		goodCapsule("fontSize", glass), goodCapsule("fontBigger", glass), goodCapsule("fold", glass),
		goodCapsule("fleetButton", glass), goodPanel("fleetList", glass),
	}}
}

func goodBoardControls(glass string) controlsReport {
	return controlsReport{Surface: "board", Report: "controls", Glass: glass, Controls: []control{
		goodPanel("newCard", glass), goodCapsule("newCardTitle", glass), goodCapsule("newCardZone", glass),
		goodCapsule("newCardCreate", glass), goodCapsule("newCardCancel", glass),
	}}
}

// controlsLog is a stand's log: the frame drawn in glass, then each surface's
// controls, as the window logs them.
func controlsLog(t *testing.T, glass string, reports ...controlsReport) string {
	t.Helper()
	f := goodFrame(false)
	f.Glass = glass
	return controlsLogOf(t, f, reports...)
}

func controlsLogOf(t *testing.T, f frameReport, reports ...controlsReport) string {
	t.Helper()
	log := logLine(t, "11:00:00.000000", f)
	for _, r := range reports {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		words := "the board reports its controls: "
		if r.Surface == "orchestrator" {
			words = "the orchestrator surface reports its controls: "
		}
		log += "2026/09/15 11:00:01.000000 fleetdeck-window: " + words + string(raw) + "\n"
	}
	return log
}

var bothOpen = []string{"newcard", "fleetmenu"}

func TestCapsulesThatKeepTheirPropertiesAreNoProblem(t *testing.T) {
	for _, glass := range []string{"glass", "vibrancy", "opaque"} {
		log := controlsLog(t, glass, goodOrchestratorControls(glass), goodBoardControls(glass))
		if got := controlsCheck(log, bothOpen); len(got) != 0 {
			t.Errorf("%s: problems %q, want none", glass, got)
		}
	}
}

// A capsule on glass drawn solid hides the glass; one with no glass drawn
// see-through shows what reduced transparency asked not to show.
func TestACapsuleOfTheWrongOpacityForTheMaterialIsAProblem(t *testing.T) {
	o := goodOrchestratorControls("glass")
	o.Controls[1].FillAlpha = 1
	wantProblem(t, controlsCheck(controlsLog(t, "glass", o, goodBoardControls("glass")), bothOpen), "picker", "see-through")

	b := goodBoardControls("opaque")
	b.Controls[1].FillAlpha = 0.52
	wantProblem(t, controlsCheck(controlsLog(t, "opaque", goodOrchestratorControls("opaque"), b), bothOpen), "newCardTitle", "solid")
}

func TestACapsuleThatIsNotRoundIsAProblem(t *testing.T) {
	o := goodOrchestratorControls("glass")
	o.Controls[6].Radius = 4
	wantProblem(t, controlsCheck(controlsLog(t, "glass", o, goodBoardControls("glass")), bothOpen), "fleetButton", "capsule")
}

// Glass on glass: a capsule lying on the island blurs nothing.
func TestACapsuleThatBlursIsAProblem(t *testing.T) {
	o := goodOrchestratorControls("glass")
	o.Controls[0].Backdrop = "blur(12px)"
	wantProblem(t, controlsCheck(controlsLog(t, "glass", o, goodBoardControls("glass")), bothOpen), "editPencil", "blur")
}

func TestAFloatingPanelNotFrostedOnGlassOrBlurredWithNoGlassIsAProblem(t *testing.T) {
	b := goodBoardControls("glass")
	b.Controls[0].Backdrop = "none"
	wantProblem(t, controlsCheck(controlsLog(t, "glass", goodOrchestratorControls("glass"), b), bothOpen), "newCard", "blur")

	o := goodOrchestratorControls("opaque")
	o.Controls[7].Backdrop, o.Controls[7].FillAlpha = "blur(24px) saturate(160%)", 0.8
	problems := controlsCheck(controlsLog(t, "opaque", o, goodBoardControls("opaque")), bothOpen)
	wantProblem(t, problems, "fleetList", "blur")
	wantProblem(t, problems, "fleetList", "solid")
}

// The text is held to 4.5:1 over the worst ground under it; a disabled control
// is not read.
func TestTextUnderFourAndAHalfToOneIsAProblemUnlessDisabled(t *testing.T) {
	o := goodOrchestratorControls("glass")
	o.Controls[1].Contrast = 3.9
	wantProblem(t, controlsCheck(controlsLog(t, "glass", o, goodBoardControls("glass")), bothOpen), "picker", "3.9")

	o = goodOrchestratorControls("glass")
	o.Controls[4].Contrast, o.Controls[4].Disabled = 1.6, true
	if got := controlsCheck(controlsLog(t, "glass", o, goodBoardControls("glass")), bothOpen); len(got) != 0 {
		t.Fatalf("problems %q, want none: a disabled control is not read", got)
	}
}

// What the stand opened has to be reported, or nothing was held to anything.
func TestWhatTheStandOpenedButDidNotReportIsAProblem(t *testing.T) {
	o := goodOrchestratorControls("glass")
	o.Controls = o.Controls[:7]
	b := goodBoardControls("glass")
	b.Controls = nil
	problems := controlsCheck(controlsLog(t, "glass", o, b), bothOpen)
	wantProblem(t, problems, "fleetList")
	wantProblem(t, problems, "newCard")
	if got := controlsCheck(controlsLog(t, "glass", o, b), nil); len(got) != 0 {
		t.Fatalf("problems %q, want none: the stand opened nothing", got)
	}
}

// Review of #185: folded, the island is a strip with no head, and its capsules
// are not on screen to report.
func TestAFoldedIslandIsNotAskedForItsHead(t *testing.T) {
	o := goodOrchestratorControls("glass")
	o.Controls = nil
	log := controlsLogOf(t, foldedOrchestrator(goodFrame(false), 89), o, goodBoardControls("glass"))
	if got := controlsCheck(log, nil); len(got) != 0 {
		t.Fatalf("problems %q, want none: a folded island shows no head", got)
	}
}

// Review of #185: a stand's form opens empty, and its title shows the
// placeholder.
func TestAPlaceholderUnderFourAndAHalfToOneIsAProblem(t *testing.T) {
	b := goodBoardControls("glass")
	b.Controls[1].PlaceholderContrast = at(2.3)
	wantProblem(t, controlsCheck(controlsLog(t, "glass", goodOrchestratorControls("glass"), b), bothOpen), "newCardTitle", "placeholder", "2.3")
	b.Controls[1].PlaceholderContrast = at(6.1)
	if got := controlsCheck(controlsLog(t, "glass", goodOrchestratorControls("glass"), b), bothOpen); len(got) != 0 {
		t.Fatalf("problems %q, want none", got)
	}
}

// #185's stand on macOS 26: that WebKit answers ::placeholder with the field's
// own style, and the page says it could not measure the placeholder. That is
// said, not failed: the stand's WebKit decides it.
func TestAnUnmeasuredPlaceholderIsSaidAndNotFailed(t *testing.T) {
	b := goodBoardControls("glass")
	unmeasured := false
	b.Controls[1].PlaceholderMeasured = &unmeasured
	log := controlsLog(t, "glass", goodOrchestratorControls("glass"), b)
	if got := controlsCheck(log, bothOpen); len(got) != 0 {
		t.Fatalf("problems %q, want none", got)
	}
	notes := controlsNotes(log)
	if len(notes) != 1 || !strings.Contains(notes[0], "newCardTitle placeholder was not measured") {
		t.Fatalf("notes %q, want one naming the title's placeholder", notes)
	}
	measured := true
	b.Controls[1].PlaceholderMeasured, b.Controls[1].PlaceholderContrast = &measured, at(6.1)
	if got := controlsNotes(controlsLog(t, "glass", goodOrchestratorControls("glass"), b)); len(got) != 0 {
		t.Fatalf("notes %q, want none for a measured placeholder", got)
	}
}

// A disabled capsule is dimmed on purpose (web/app.css): its fill is not held to
// the material, as its text is not held to 4.5:1.
func TestADimmedDisabledCapsuleIsNoProblem(t *testing.T) {
	b := goodBoardControls("opaque")
	b.Controls[3].FillAlpha, b.Controls[3].Contrast, b.Controls[3].Disabled = 0.45, 2.1, true
	if got := controlsCheck(controlsLog(t, "opaque", goodOrchestratorControls("opaque"), b), bothOpen); len(got) != 0 {
		t.Fatalf("problems %q, want none", got)
	}
}

func TestTheIslandsHeadCapsulesHaveToBeReported(t *testing.T) {
	o := goodOrchestratorControls("glass")
	o.Controls = []control{goodCapsule("fontSize", "glass")}
	problems := controlsCheck(controlsLog(t, "glass", o, goodBoardControls("glass")), nil)
	for _, name := range []string{"editPencil", "picker", "fleetButton"} {
		wantProblem(t, problems, name)
	}
}

func TestNoControlsReportIsAProblem(t *testing.T) {
	problems := controlsCheck(controlsLog(t, "glass"), nil)
	wantProblem(t, problems, "orchestrator", "no controls report")
	wantProblem(t, problems, "board", "no controls report")
}

// The page draws for the material the window sent it; one drawing for another
// has capsules of the wrong kind whatever each measures.
func TestAPageDrawingForAnotherMaterialIsAProblem(t *testing.T) {
	log := controlsLog(t, "opaque", goodOrchestratorControls("glass"), goodBoardControls("opaque"))
	wantProblem(t, controlsCheck(log, nil), "orchestrator", "glass", "opaque")
}

// A report is taken as the page lays out, again and again: the last one is what
// the frame shows.
func TestOnlyTheLastControlsReportCounts(t *testing.T) {
	bad := goodOrchestratorControls("glass")
	bad.Controls[1].Radius = 4
	log := controlsLog(t, "glass", bad, goodOrchestratorControls("glass"), goodBoardControls("glass"))
	if got := controlsCheck(log, bothOpen); len(got) != 0 {
		t.Fatalf("problems %q, want none", got)
	}
}

func TestTheStandsOpenListIsReadAsTheWindowReadsIt(t *testing.T) {
	if got := openList("newcard,fleetmenu"); strings.Join(got, "|") != "newcard|fleetmenu" {
		t.Fatalf("got %q", got)
	}
	if got := openList(""); len(got) != 0 {
		t.Fatalf("got %q, want nothing open", got)
	}
}
