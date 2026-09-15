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
// has no web views, so a surface is only the frame being up, and the board is
// only the last insets it was sent.
type standNatives struct {
	f      *frame
	ctl    *controller
	mode   glassMode
	model  json.RawMessage
	framed bool
	insets map[string]any
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

func (n *standNatives) boardInsets() { n.run(n.ctl.boardInsetsNow()) }

func (n *standNatives) send(surface string, msg map[string]any) {
	if surface == "board" && msg["type"] == "insets" {
		n.insets = msg
	}
}

func (n *standNatives) focus(string)           {}
func (n *standNatives) navigateBoard(string)   {}
func (n *standNatives) openExternal(string)    {}
func (n *standNatives) reloadSurface(string)   {}
func (n *standNatives) showWindowPage(string)  {}
func (n *standNatives) setAppearance(string)   {}
func (n *standNatives) saveWidths(panelWidths) {}
func (n *standNatives) reloadBoard()           {}
func (n *standNatives) setDragBand(float64)    {}

// standFrame is what is on the screen: the window's width as laid out, the
// panels' widths and inner edges, the row's width and minimum, its capsules,
// and the last insets the board was sent.
type standFrame struct {
	width                           float64
	orchestrator, sessions          float64
	orchestratorRight, sessionsLeft float64
	row, rowMin                     float64
	capsules                        []drawnCapsule
	insetsSent                      bool
	insetsLeft, insetsContentRight  float64
}

// capsuleStandProbe is the stand's window, 1000 by 700 with both panels
// unfolded at 368 and 348: once the board has given its capsules and said it
// is a panel page, again after the window says it was resized, after the frame
// was taken down and came up again, and after the board gave longer labels;
// then in full screen on a wider screen, after a drag on the sessions panel's
// edge was let go, and with the sessions panel folded.
type capsuleStandProbe struct {
	afterLayout, afterResize, afterReframe, afterLongerLabels standFrame
	afterFullScreen, afterDragRelease, afterFold              standFrame
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
	out.afterLayout = readStandFrame(n)

	width, height := windowContentSize(window)
	n.run(ctl.resized(width, height, false))
	out.afterResize = readStandFrame(n)

	n.run(ctl.boardShowsOwnPage())
	n.run(ctl.layout(hostVersion, "panel", "stand"))
	out.afterReframe = readStandFrame(n)

	longer := m
	longer.NewCard.Label += " флота"
	if raw, err = json.Marshal(longer); err != nil {
		panic(err)
	}
	n.run(ctl.capsules(raw))
	out.afterLongerLabels = readStandFrame(n)

	n.run(ctl.resized(1728, 1117, true))
	out.afterFullScreen = readStandFrame(n)

	if start, ok := ctl.resizeStart("sessions"); ok {
		n.run(ctl.resizeTo("sessions", start, -60))
	}
	n.run(ctl.resizeEnd())
	out.afterDragRelease = readStandFrame(n)

	n.run(ctl.panel("sessions", true))
	out.afterFold = readStandFrame(n)
	return out
}

// foldedStandFrame is the frame as measured (measureFrame) with the panels
// folded as when says.
type foldedStandFrame struct {
	when  string
	frame standFrameReport
}

// probeFoldsForTest is a stand's window, 1000 by 700, started with the
// orchestrator panel folded, its buttons measured once the frame is up, as
// glassWindow measures them whenever the window changes; then with neither
// panel folded, the sessions panel folded, both, and the orchestrator panel
// alone again, each folded or unfolded as a surface does.
func probeFoldsForTest(m capsuleModel) []foldedStandFrame {
	window := C.fd_test_window(1000, 700)
	f := installFrame(window)
	f.setMode(glassModeGlass)
	ctl := newController("http://127.0.0.1:7777/", panelWidths{Orchestrator: 368, Sessions: 348, OrchestratorFolded: true}, glassModeGlass)
	n := &standNatives{f: f, ctl: ctl, mode: glassModeGlass}
	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	width, height := windowContentSize(window)
	n.run(ctl.resized(width, height, false))
	n.run(ctl.capsules(raw))
	n.run(ctl.layout(hostVersion, "panel", "stand"))
	n.run(ctl.titlebarButtons(f.titlebarInset(), f.titlebarCenter()))
	out := []foldedStandFrame{{"started with the orchestrator panel folded", measureFrame(f, window, glassModeGlass)}}
	for _, s := range []struct {
		side   string
		folded bool
		when   string
	}{
		{"orchestrator", false, "with neither panel folded"},
		{"sessions", true, "with the sessions panel folded"},
		{"orchestrator", true, "with both panels folded"},
		{"sessions", false, "with the orchestrator panel folded again"},
	} {
		n.run(ctl.panel(s.side, s.folded))
		out = append(out, foldedStandFrame{s.when, measureFrame(f, window, glassModeGlass)})
	}
	return out
}

func readStandFrame(n *standNatives) standFrame {
	o := C.fd_test_frame_of(C.fd_test_panel(n.f.p, 0))
	s := C.fd_test_frame_of(C.fd_test_panel(n.f.p, 1))
	out := standFrame{
		width:             n.ctl.width,
		orchestrator:      float64(o.w),
		sessions:          float64(s.w),
		orchestratorRight: float64(o.x + o.w),
		sessionsLeft:      float64(s.x),
		row:               float64(C.fd_test_row_width()),
		rowMin:            n.ctl.rowMin,
	}
	if n.insets != nil {
		out.insetsSent = true
		out.insetsLeft, _ = n.insets["left"].(float64)
		out.insetsContentRight, _ = n.insets["contentRight"].(float64)
	}
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		fr := C.fd_test_capsule_slot_frame(C.int(i))
		out.capsules = append(out.capsules, drawnCapsule{
			name: C.GoString(C.fd_test_capsule_slot_name(C.int(i))), x: float64(fr.x), w: float64(fr.w), visible: fr.visible != 0,
		})
	}
	return out
}
