package main

// The frame is built once, on the main thread, in TestMain
// (menu_darwin_test.go), on a window never put on screen; the tests below only
// read what it found. See menu_darwin_test.go for why.

import (
	"math"
	"testing"
)

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

// The frame runs under the title bar: the capsules' 10 pt and the panels' 8 pt
// are from the window's top edge, with the window's buttons over the
// orchestrator panel's corner. On the macos-15 runner of PR #166 the root kept
// the content area it replaced, and the title bar's 28 pt above it were black.
func TestTheFrameCoversTheWholeWindowUnderTheTitleBar(t *testing.T) {
	r := frameResult
	whole := rect{W: r.windowFrame.W, H: r.windowFrame.H}
	if r.rootFrame != whole {
		t.Fatalf("root frame %+v, want the whole window %+v", r.rootFrame, whole)
	}
	if r.boardFrame != whole {
		t.Fatalf("board frame %+v, want the whole window %+v", r.boardFrame, whole)
	}
	if r.contentWidth != whole.W || r.contentHeight != whole.H {
		t.Fatalf("the geometry is laid out for %vx%v, want the whole window %vx%v", r.contentWidth, r.contentHeight, whole.W, whole.H)
	}
}

// Putting the frame in leaves the window where and as large as it was. On the
// macos-15 runner of PR #166 the window came up 28 pt lower and shorter than
// v0.9.1's, the desktop showing above it: the title bar's height, lost when the
// style changed.
func TestInstallingTheFrameKeepsTheWindowsFrame(t *testing.T) {
	if frameResult.windowFrame != frameResult.windowFrameBefore {
		t.Fatalf("window frame %+v after the frame went in, want %+v as before", frameResult.windowFrame, frameResult.windowFrameBefore)
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

// v0.10.1 on the operator's macOS: with no toolbar the window's buttons sat
// where a plain title bar puts them, centred 16 pt from the window's top left,
// on the orchestrator panel's edge 8 pt in, and above the brand. A window with a
// unified toolbar centres them 26 pt in, which is where the panel's 18 pt corner
// curves around: the buttons sit concentric in it, as in a floating sidebar. The
// window says that line to the surface, and the material does not move it.
func TestTheWindowsButtonsSitConcentricInTheOrchestratorPanelsCorner(t *testing.T) {
	r := frameResult
	cx, cy := r.closeButton.X+r.closeButton.W/2, r.closeButton.Y+r.closeButton.H/2
	want := panelMargin + 18
	if r.closeButton.W == 0 || math.Abs(cx-want) > 1 || math.Abs(cy-want) > 1 {
		t.Errorf("the close button is centred at (%v, %v), want (%v, %v) in the orchestrator panel's corner", cx, cy, want, want)
	}
	if math.Abs(r.titlebarCenter-cy) > 0.01 {
		t.Errorf("the window says its buttons are centred %v pt down, they are at %v", r.titlebarCenter, cy)
	}
	if r.closeButtonOpaque != r.closeButton {
		t.Errorf("the close button is at %+v with the panels opaque, %+v on glass", r.closeButtonOpaque, r.closeButton)
	}
}

// The toolbar is there only to place the buttons: no items, and the title bar
// over it transparent, so it draws nothing over the capsules or the board.
func TestTheToolbarThatPlacesTheButtonsIsEmptyUnifiedAndUnderATransparentTitleBar(t *testing.T) {
	r := frameResult
	if r.toolbarItems != 0 {
		t.Errorf("the window's toolbar has %d items (-1: no toolbar), want an empty toolbar", r.toolbarItems)
	}
	if r.toolbarStyle != 3 {
		t.Errorf("toolbar style %d, want 3 (NSWindowToolbarStyleUnified)", r.toolbarStyle)
	}
	if !r.titlebarTransparent {
		t.Error("the title bar is not transparent over the toolbar")
	}
}

func TestChangingTheMaterialKeepsWhatIsInsideThePanel(t *testing.T) {
	if !frameResult.contentKept {
		t.Fatal("the panel's content view was replaced with its wrapper: the surface web view inside would be lost")
	}
}
