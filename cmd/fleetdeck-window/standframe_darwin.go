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

// standFrameReport is the frame as AppKit laid it out, in points from the
// window's top left: its material and full screen, the close button, both
// panels, the capsule row and each capsule shown, and the tabs' border shape
// and selected segment's shape (capsules_darwin.c).
type standFrameReport struct {
	Glass              string            `json:"glass"`
	FullScreen         bool              `json:"fullScreen"`
	Close              measuredBox       `json:"close"`
	Orchestrator       measuredBox       `json:"orchestrator"`
	Sessions           measuredBox       `json:"sessions"`
	Row                measuredBox       `json:"row"`
	Capsules           []measuredCapsule `json:"capsules"`
	SegmentBorderShape int               `json:"segmentBorderShape"`
	SelectedTopInset   float64           `json:"selectedTopInset"`
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
		Close:              boxOf(C.fd_test_window_button(window, 0)),
		Orchestrator:       boxOf(C.fd_test_frame_of(C.fd_test_panel(f.p, 0))),
		Sessions:           boxOf(C.fd_test_frame_of(C.fd_test_panel(f.p, 1))),
		Row:                boxOf(C.fd_test_frame_of(f.capsules())),
		SegmentBorderShape: int(C.fd_test_segment_border_shape()),
		SelectedTopInset:   float64(C.fd_test_selected_segment_top_inset()),
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
