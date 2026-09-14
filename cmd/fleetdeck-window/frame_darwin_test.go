package main

// The frame is built once, on the main thread, in TestMain
// (menu_darwin_test.go), on a window never put on screen; the tests below only
// read what it found. See menu_darwin_test.go for why.

import "testing"

var (
	frameGeometry = layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348})
	frameResult   frameProbe
)

func collectFrameResults() {
	frameResult = probeFrameForTest(frameGeometry)
}

func TestPanelsAreRegularGlassWithTheDesignRadius(t *testing.T) {
	if !frameResult.glassAvailable {
		t.Skip("NSGlassEffectView is not on this system; the vibrancy test covers it")
	}
	for side, name := range []string{"orchestrator", "sessions"} {
		if frameResult.classes[side] != "NSGlassEffectView" {
			t.Fatalf("%s panel is %s", name, frameResult.classes[side])
		}
		if frameResult.styles[side] != 0 {
			t.Fatalf("%s panel style = %d, want 0 (Regular): Clear makes the text under it unreadable", name, frameResult.styles[side])
		}
		if frameResult.radii[side] != 18 {
			t.Fatalf("%s panel radius = %v", name, frameResult.radii[side])
		}
	}
}

func TestTheBoardStaysUnderneathThePanelsAndTheCapsulesOnTop(t *testing.T) {
	r := frameResult
	if r.boardIndex != 0 {
		t.Fatalf("board is subview %d of the root, want 0", r.boardIndex)
	}
	for side, index := range r.panelIndexes {
		if index <= r.boardIndex || index >= r.capsuleIndex {
			t.Fatalf("panel %d is subview %d, board %d, capsules %d", side, index, r.boardIndex, r.capsuleIndex)
		}
	}
}

func TestAPanelTakesThePlaceTheGeometryGivesIt(t *testing.T) {
	if frameResult.orchestratorFrame != frameGeometry.Orchestrator {
		t.Fatalf("orchestrator panel frame = %+v, want %+v", frameResult.orchestratorFrame, frameGeometry.Orchestrator)
	}
}

func TestAClickBetweenTheCapsulesReachesTheBoard(t *testing.T) {
	if !frameResult.capsulesPassThrough {
		t.Fatal("the capsule row takes a click where it has no capsule, and the board under it never gets it")
	}
}

func TestVibrancyBlursTheBoardNotTheDesktop(t *testing.T) {
	r := frameResult
	if r.vibrancyClass != "NSVisualEffectView" || r.vibrancyBlending != 1 || r.vibrancyMaterial != 7 {
		t.Fatalf("vibrancy panel = %s blending %d material %d, want NSVisualEffectView withinWindow (1) sidebar (7)", r.vibrancyClass, r.vibrancyBlending, r.vibrancyMaterial)
	}
}

func TestOpaqueModeLeavesNoMaterialBehindTheSurfaces(t *testing.T) {
	if frameResult.opaqueClass != "NSView" {
		t.Fatalf("opaque panel = %s, want a plain NSView", frameResult.opaqueClass)
	}
}

func TestTheViewASurfaceGoesIntoFillsItsPanelAndFollowsItsWidth(t *testing.T) {
	if !frameResult.contentFitsPanel {
		t.Fatal("the panel's content view is not the panel's size: a surface web view made in it has the wrong size")
	}
	if !frameResult.contentFollowsDrag {
		t.Fatal("the panel's content view kept its width when the panel's edge was dragged")
	}
}

func TestAnUnfoldedPanelHasAnEdgeToDragAndAFoldedOneHasNone(t *testing.T) {
	if !frameResult.stripShown {
		t.Fatal("the orchestrator panel has no strip at its edge to drag its width")
	}
	if !frameResult.stripOfFoldedHidden {
		t.Fatal("the folded sessions panel shows a strip to drag a width it does not have")
	}
}

func TestDraggingThePanelsEdgeChangesItsWidthWithinLimits(t *testing.T) {
	r := frameResult
	if r.draggedWidth != 418 {
		t.Fatalf("after a 50 pt drag right: width = %v, want 418", r.draggedWidth)
	}
	if r.clampedWidth != 220 {
		t.Fatalf("after a drag far past the floor: width = %v, want 220", r.clampedWidth)
	}
	if len(r.savedWidths) != 2 || r.savedWidths[0].Orchestrator != 418 || r.savedWidths[1].Orchestrator != 220 {
		t.Fatalf("widths kept on release = %+v, want 418 then 220", r.savedWidths)
	}
}

func TestChangingTheMaterialKeepsWhatIsInsideThePanel(t *testing.T) {
	if !frameResult.contentKept {
		t.Fatal("the panel's content view was replaced with its wrapper: the surface web view inside would be lost")
	}
}
