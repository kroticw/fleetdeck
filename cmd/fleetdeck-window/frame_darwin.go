package main

/*
#cgo LDFLAGS: -framework Cocoa
#include "frame_darwin.h"
#include <stdlib.h>
*/
import "C"

import (
	"sync"
	"unsafe"
)

// frame is the glass frame over the board's web view (frame_darwin.c). Every
// method is AppKit and runs on the main thread.
type frame struct{ p unsafe.Pointer }

// installFrame puts the frame into window, the pointer webview's Window()
// returns, around the web view that is its content view. The panels take their
// material from setMode and their place from layout.
func installFrame(window unsafe.Pointer) *frame {
	return &frame{p: C.fd_frame_install(window)}
}

func (f *frame) setMode(m glassMode) {
	mode := C.CString(string(m))
	defer C.free(unsafe.Pointer(mode))
	C.fd_frame_set_mode(f.p, mode)
}

func (f *frame) layout(g geometry) {
	C.fd_frame_layout(f.p, fdRect(g.Orchestrator), fdRect(g.Sessions), fdRect(g.Capsules),
		cBool(g.OrchestratorResizable), cBool(g.SessionsResizable))
}

func cBool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// resizeEvents is where a drag on a panel's edge goes: glasswindow.go hands it
// to the controller. phase is 0 for the press, 1 for a drag, 2 for the release;
// x is the pointer's, in the window.
var resizeEvents = struct {
	sync.Mutex
	drag func(side string, phase int, x float64)
}{}

func setResizeEvents(drag func(side string, phase int, x float64)) {
	resizeEvents.Lock()
	defer resizeEvents.Unlock()
	resizeEvents.drag = drag
}

//export fleetdeckResize
func fleetdeckResize(side *C.char, phase C.int, x C.double) {
	resizeEvents.Lock()
	drag := resizeEvents.drag
	resizeEvents.Unlock()
	if drag != nil {
		drag(C.GoString(side), int(phase), float64(x))
	}
}

// panelContent is the view a side surface's web view goes into.
func (f *frame) panelContent(side string) unsafe.Pointer {
	if side == "sessions" {
		return C.fd_frame_panel_content(f.p, 1)
	}
	return C.fd_frame_panel_content(f.p, 0)
}

func (f *frame) capsules() unsafe.Pointer { return C.fd_frame_capsules(f.p) }
func (f *frame) board() unsafe.Pointer    { return C.fd_frame_board(f.p) }

func fdRect(r rect) C.fd_rect {
	return C.fd_rect{x: C.double(r.X), y: C.double(r.Y), w: C.double(r.W), h: C.double(r.H)}
}

// currentGlassMode is what the system allows under the side surfaces now. With
// reduced transparency there is nothing; increased contrast is treated the same
// until a stand shows macOS 26 turning transparency down with it (spec 7.2).
func currentGlassMode() glassMode {
	switch {
	case C.fd_reduce_transparency() != 0 || C.fd_increase_contrast() != 0:
		return glassModeOpaque
	case C.fd_glass_available() == 0:
		return glassModeVibrancy
	default:
		return glassModeGlass
	}
}

// What frame_darwin_test.go reads; Go test files cannot use cgo.

type frameProbe struct {
	contentFitsPanel    bool
	contentFollowsDrag  bool
	stripShown          bool
	stripOfFoldedHidden bool
	draggedWidth        float64
	clampedWidth        float64
	savedWidths         []panelWidths
	glassAvailable      bool
	classes             [2]string
	styles              [2]int
	radii               [2]float64
	boardIndex          int
	panelIndexes        [2]int
	capsuleIndex        int
	orchestratorFrame   rect
	contentKept         bool
	capsulesPassThrough bool
	vibrancyClass       string
	vibrancyBlending    int
	vibrancyMaterial    int
	opaqueClass         string
	board, capsulesView unsafe.Pointer
}

func probeFrameForTest(g geometry) frameProbe {
	var out frameProbe
	window := C.fd_test_window(1512, 982)
	f := installFrame(window)
	f.layout(g)
	root := C.fd_test_root(f.p)
	out.glassAvailable = C.fd_glass_available() != 0
	f.setMode(glassModeGlass)
	content := f.panelContent("orchestrator")
	fits := C.fd_test_frame_of(content)
	out.contentFitsPanel = float64(fits.w) == g.Orchestrator.W && float64(fits.h) == g.Orchestrator.H
	for side := 0; side < 2; side++ {
		panel := C.fd_test_panel(f.p, C.int(side))
		out.classes[side] = C.GoString(C.fd_test_class_name(panel))
		if out.glassAvailable {
			out.styles[side] = int(C.fd_test_glass_style(panel))
		}
		out.radii[side] = float64(C.fd_test_corner_radius(panel))
		out.panelIndexes[side] = int(C.fd_test_subview_index(root, panel))
	}
	out.board, out.capsulesView = f.board(), f.capsules()
	out.boardIndex = int(C.fd_test_subview_index(root, out.board))
	out.capsuleIndex = int(C.fd_test_subview_index(root, out.capsulesView))
	r := C.fd_test_frame_of(C.fd_test_panel(f.p, 0))
	out.orchestratorFrame = rect{X: float64(r.x), Y: float64(r.y), W: float64(r.w), H: float64(r.h)}
	out.capsulesPassThrough = C.fd_test_passes_through(out.capsulesView) != 0

	f.setMode(glassModeVibrancy)
	vib := C.fd_test_panel(f.p, 0)
	out.vibrancyClass = C.GoString(C.fd_test_class_name(vib))
	out.vibrancyBlending = int(C.fd_test_blending_mode(vib))
	out.vibrancyMaterial = int(C.fd_test_material(vib))

	f.setMode(glassModeOpaque)
	out.opaqueClass = C.GoString(C.fd_test_class_name(C.fd_test_panel(f.p, 0)))
	out.contentKept = f.panelContent("orchestrator") == content

	// The edge dragged through the strip's own mouse methods, into a
	// controller, and back into the frame.
	ctl := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348}, glassModeOpaque)
	ctl.resized(1512, 982, false)
	ctl.layout(1, "panel", "work")
	var start, pressedAt float64
	setResizeEvents(func(side string, phase int, x float64) {
		var effects []effect
		switch phase {
		case 0:
			start, _ = ctl.resizeStart(side)
			pressedAt = x
		case 1:
			effects = ctl.resizeTo(side, start, x-pressedAt)
		case 2:
			effects = ctl.resizeEnd()
		}
		for _, e := range effects {
			switch e := e.(type) {
			case applyGeometry:
				f.layout(e.G)
			case saveWidths:
				out.savedWidths = append(out.savedWidths, e.W)
			}
		}
	})
	// On glass, whose content view is the one AppKit does not size by itself.
	f.setMode(glassModeGlass)
	strip := C.fd_test_strip(f.p, 0)
	out.stripShown = C.fd_test_is_hidden(strip) == 0
	C.fd_test_mouse(strip, 0, 380)
	C.fd_test_mouse(strip, 1, 430)
	C.fd_test_mouse(strip, 2, 430)
	out.draggedWidth = float64(C.fd_test_frame_of(C.fd_test_panel(f.p, 0)).w)
	out.contentFollowsDrag = float64(C.fd_test_frame_of(f.panelContent("orchestrator")).w) == out.draggedWidth
	C.fd_test_mouse(strip, 0, 426)
	C.fd_test_mouse(strip, 1, -1000)
	C.fd_test_mouse(strip, 2, -1000)
	out.clampedWidth = float64(C.fd_test_frame_of(C.fd_test_panel(f.p, 0)).w)
	setResizeEvents(nil)

	f.layout(layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348, SessionsFolded: true}))
	out.stripOfFoldedHidden = C.fd_test_is_hidden(C.fd_test_strip(f.p, 1)) != 0
	return out
}
