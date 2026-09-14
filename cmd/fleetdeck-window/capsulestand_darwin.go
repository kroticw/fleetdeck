package main

/*
#include "capsules_darwin.h"
#include "frame_darwin.h"
*/
import "C"

import "encoding/json"

// What capsules_darwin_test.go reads of the capsule row on the path a window on
// a stand takes: the controller's decisions carried out by runEffects on a
// real frame, in the order they come. v0.10.1's first stand showed a row the
// tests of its parts never saw.

// standNatives carries effects out as glassWindow does, on a hidden window's
// frame: the panels laid out, the capsules drawn through drawCapsuleRow. It
// has no web views, so a surface is only the frame being up.
type standNatives struct {
	f      *frame
	ctl    *controller
	mode   glassMode
	model  json.RawMessage
	framed bool
}

func (n *standNatives) run(effects []effect) { runEffects(n, effects) }

func (n *standNatives) redraw() {
	if n.framed && n.model != nil {
		drawCapsuleRow(n.f, n.model, n.mode, n.ctl, n.run)
	}
}

func (n *standNatives) createSurface(string, string, glassMode) {
	n.framed = true
	n.redraw()
}

func (n *standNatives) destroySurfaces() {
	n.framed = false
	n.f.layout(geometry{})
	clearCapsules(n.f.capsules())
}

func (n *standNatives) applyGeometry(g geometry) {
	n.f.layout(g)
	n.run(n.ctl.laidOut(g))
}

func (n *standNatives) setCapsules(model json.RawMessage) {
	n.model = model
	n.redraw()
}

func (n *standNatives) setFrameMode(m glassMode) {
	n.mode = m
	n.f.setMode(m)
	n.redraw()
}

func (n *standNatives) send(string, map[string]any) {}
func (n *standNatives) focus(string)                {}
func (n *standNatives) navigateBoard(string)        {}
func (n *standNatives) openExternal(string)         {}
func (n *standNatives) reloadSurface(string)        {}
func (n *standNatives) showWindowPage(string)       {}
func (n *standNatives) setAppearance(string)        {}
func (n *standNatives) saveWidths(panelWidths)      {}
func (n *standNatives) reloadBoard()                {}
func (n *standNatives) setDragBand(float64)         {}

// standFrame is what is on the screen: the panels' widths, the row's width and
// minimum, and its capsules.
type standFrame struct {
	orchestrator, sessions float64
	row, rowMin            float64
	capsules               []drawnCapsule
}

// capsuleStandProbe is the stand's window, 1000 by 700 with both panels
// unfolded at 368 and 348: once the board has given its capsules and said it
// is a panel page, again after the window says it was resized, after the frame
// was taken down and came up again, and after the board gave longer labels.
type capsuleStandProbe struct {
	afterLayout, afterResize, afterReframe, afterLongerLabels standFrame
}

func probeCapsuleStandForTest(m capsuleModel) capsuleStandProbe {
	var out capsuleStandProbe
	window := C.fd_test_window(1000, 700)
	f := installFrame(window)
	f.setMode(glassModeGlass)
	ctl := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348}, glassModeGlass)
	n := &standNatives{f: f, ctl: ctl, mode: glassModeGlass}
	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}

	n.run(ctl.resized(1000, 700, false))
	n.run(ctl.capsules(raw))
	n.run(ctl.layout(hostVersion, "panel", "stand"))
	out.afterLayout = readStandFrame(f, ctl)

	width, height := windowContentSize(window)
	n.run(ctl.resized(width, height, false))
	out.afterResize = readStandFrame(f, ctl)

	n.run(ctl.boardShowsOwnPage())
	n.run(ctl.layout(hostVersion, "panel", "stand"))
	out.afterReframe = readStandFrame(f, ctl)

	longer := m
	longer.NewCard.Label += " флота"
	if raw, err = json.Marshal(longer); err != nil {
		panic(err)
	}
	n.run(ctl.capsules(raw))
	out.afterLongerLabels = readStandFrame(f, ctl)
	return out
}

func readStandFrame(f *frame, ctl *controller) standFrame {
	out := standFrame{
		orchestrator: float64(C.fd_test_frame_of(C.fd_test_panel(f.p, 0)).w),
		sessions:     float64(C.fd_test_frame_of(C.fd_test_panel(f.p, 1)).w),
		row:          float64(C.fd_test_row_width()),
		rowMin:       ctl.rowMin,
	}
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		fr := C.fd_test_capsule_slot_frame(C.int(i))
		out.capsules = append(out.capsules, drawnCapsule{
			name: C.GoString(C.fd_test_capsule_slot_name(C.int(i))), x: float64(fr.x), w: float64(fr.w), visible: fr.visible != 0,
		})
	}
	return out
}
