package main

/*
#include "capsules_darwin.h"
#include "frame_darwin.h"
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

// frameMeasuresPrefix is how the window's log starts what a stand's frame
// measures natively. scripts/standcheck reads the lines and holds them to the
// properties the operator saw broken in v0.10.1; standframe_darwin_test.go
// pins the words and the fields it reads.
const frameMeasuresPrefix = "fleetdeck-window: the frame measures "

type measuredBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type measuredCapsule struct {
	Name string `json:"name"`
	measuredBox
}

// measuredOverlay is a view or window that may lie over the content: the title
// bar's container, or another window of the app over this one.
type measuredOverlay struct {
	Kind string `json:"kind"`
	measuredBox
	Visible bool    `json:"visible"`
	Alpha   float64 `json:"alpha"`
}

// standFrameReport is the frame as AppKit laid it out, in points from the
// window's top left: its material and full screen, the close button, both
// panels, the capsule row and each capsule shown, and the tabs' border shape
// and selected segment's shape (capsules_darwin.c).
type standFrameReport struct {
	Glass      string `json:"glass"`
	FullScreen bool   `json:"fullScreen"`
	// MenuBarVisible: the menu bar is shown, as a pointer at the top of the
	// screen shows it in full screen; ToolbarVisible: the window's toolbar is.
	MenuBarVisible bool `json:"menuBarVisible"`
	ToolbarVisible bool `json:"toolbarVisible"`
	// MenuBarHeight is how far down the menu bar comes over the window's top
	// in full screen when a pointer at the top of the screen brings it out.
	MenuBarHeight float64 `json:"menuBarHeight"`
	// The window's three buttons; zero-sized when there is no such button.
	Close              measuredBox       `json:"close"`
	Minimize           measuredBox       `json:"minimize"`
	Zoom               measuredBox       `json:"zoom"`
	Orchestrator       measuredBox       `json:"orchestrator"`
	Sessions           measuredBox       `json:"sessions"`
	Row                measuredBox       `json:"row"`
	Capsules           []measuredCapsule `json:"capsules"`
	SegmentBorderShape int               `json:"segmentBorderShape"`
	SelectedTopInset   float64           `json:"selectedTopInset"`
	// RoundedTopInset is the same measure of a rounded rectangle drawn in the
	// tabs' place, what the selected tab is held against.
	RoundedTopInset float64 `json:"roundedTopInset"`
	// ContentLayoutTop is how much of the window's top its title bar and
	// toolbar keep from the content; Overlays what lies over the content
	// there. In v0.11.0's first full screen stand the toolbar stayed as a black
	// band over the capsule row.
	ContentLayoutTop float64           `json:"contentLayoutTop"`
	Overlays         []measuredOverlay `json:"overlays"`
	// DragBand is the band at the window's top the window is dragged by, as the
	// frame has it, and DragBandHit whether a press over the board in the middle
	// of that band, routed from the window, lands on it. Both are measured, not
	// the page's word: v0.11.0's window was left with no band at all and could
	// not be moved (T-076).
	DragBand      measuredBox   `json:"dragBand"`
	DragBandPress measuredPoint `json:"dragBandPress"`
	DragBandHit   bool          `json:"dragBandHit"`
}

// measuredPoint is a point in the window, in points from its top left.
type measuredPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// dragBandPress is where a press is tried for the band: over the board, halfway
// across the capsule row, and above the row itself -- a capsule there is the
// capsule's press, not the band's, however tall the band is.
func dragBandPress(row, band measuredBox) measuredPoint {
	y := band.H
	if row.H > 0 && row.Y < y {
		y = row.Y
	}
	return measuredPoint{X: row.X + row.W/2, Y: y / 2}
}

func boxOf(r C.fd_rect) measuredBox {
	return measuredBox{X: float64(r.x), Y: float64(r.y), W: float64(r.w), H: float64(r.h)}
}

// measureFrame is f in window as it is once AppKit has laid the window out --
// the capsules of v0.10.1 were in the row until that pass moved them. Main
// thread, on a stand.
func measureFrame(f *frame, window unsafe.Pointer, mode glassMode) standFrameReport {
	C.fd_test_layout_window(window)
	out := standFrameReport{
		Glass:              string(mode),
		FullScreen:         windowIsFullscreen(window),
		MenuBarVisible:     menuBarVisible(),
		ToolbarVisible:     toolbarVisible(window),
		MenuBarHeight:      menuBarHeight(),
		Close:              boxOf(C.fd_test_window_button(window, 0)),
		Minimize:           boxOf(C.fd_test_window_button(window, 1)),
		Zoom:               boxOf(C.fd_test_window_button(window, 2)),
		Orchestrator:       boxOf(C.fd_test_frame_of(C.fd_test_panel(f.p, 0))),
		Sessions:           boxOf(C.fd_test_frame_of(C.fd_test_panel(f.p, 1))),
		Row:                boxOf(C.fd_test_frame_of(f.capsules())),
		SegmentBorderShape: int(C.fd_test_segment_border_shape()),
		SelectedTopInset:   float64(C.fd_test_selected_segment_top_inset()),
		RoundedTopInset:    float64(C.fd_test_rounded_segment_top_inset()),
		ContentLayoutTop:   float64(C.fd_test_content_layout_top(window)),
		DragBand:           boxOf(C.fd_test_frame_of(C.fd_test_band(f.p))),
	}
	out.DragBandPress = dragBandPress(out.Row, out.DragBand)
	out.DragBandHit = out.DragBand.H > 0 &&
		C.fd_test_window_hit_within(window, C.double(out.DragBandPress.X), C.double(out.DragBandPress.Y), C.fd_test_band(f.p)) != 0
	var overlays [16]C.fd_overlay
	for i, n := 0, int(C.fd_test_overlays(window, &overlays[0], C.int(len(overlays)))); i < n; i++ {
		o := overlays[i]
		out.Overlays = append(out.Overlays, measuredOverlay{
			Kind:        C.GoString(&o.kind[0]),
			measuredBox: boxOf(o.r),
			Visible:     o.visible != 0,
			Alpha:       float64(o.alpha),
		})
	}
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		fr := C.fd_test_capsule_slot_frame(C.int(i))
		if fr.visible == 0 {
			continue
		}
		out.Capsules = append(out.Capsules, measuredCapsule{
			Name:        C.GoString(C.fd_test_capsule_slot_name(C.int(i))),
			measuredBox: measuredBox{X: out.Row.X + float64(fr.x), Y: out.Row.Y, W: float64(fr.w), H: capsuleHeight},
		})
	}
	return out
}

// frameReportLine is the window's log line for r.
func frameReportLine(r standFrameReport) string {
	raw, err := json.Marshal(r)
	if err != nil {
		return frameMeasuresPrefix + "{}"
	}
	return frameMeasuresPrefix + string(raw)
}
