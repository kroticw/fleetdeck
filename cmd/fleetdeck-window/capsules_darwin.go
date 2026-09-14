package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "capsules_darwin.h"
#include "frame_darwin.h"
#include "surface_darwin.h"
*/
import "C"

import (
	"sync"
	"unsafe"
)

// drawCapsules draws m into the frame's capsule row (capsules_darwin.c), in the
// panels' material. Main thread.
func drawCapsules(container unsafe.Pointer, m capsuleModel, mode glassMode) {
	var frees []unsafe.Pointer
	cstr := func(s string) *C.char {
		p := C.CString(s)
		frees = append(frees, unsafe.Pointer(p))
		return p
	}
	defer func() {
		for _, p := range frees {
			C.free(p)
		}
	}()

	tabIDs := make([]*C.char, len(m.Tabs)+1)
	tabLabels := make([]*C.char, len(m.Tabs)+1)
	selected := 0
	for i, tab := range m.Tabs {
		tabIDs[i], tabLabels[i] = cstr(tab.ID), cstr(tab.Label)
		if tab.Selected {
			selected = i
		}
	}
	n := len(m.Limits)
	limitLabels := make([]*C.char, n+1)
	limitTexts := make([]*C.char, n+1)
	values := make([]C.double, n+1)
	rgb := make([]C.double, 3*n+3)
	for i, limit := range m.Limits {
		limitLabels[i], limitTexts[i] = cstr(limit.Label), cstr(limit.Text)
		values[i] = C.double(limitValue(limit.Text))
		rgb[3*i] = -1
		if r, g, b, ok := hexColor(limit.Color); ok {
			rgb[3*i], rgb[3*i+1], rgb[3*i+2] = C.double(float64(r)/255), C.double(float64(g)/255), C.double(float64(b)/255)
		}
	}
	C.fd_capsules_draw(container, cstr(string(mode)), &tabIDs[0], &tabLabels[0], C.int(len(m.Tabs)), C.int(selected),
		cstr(m.NewCard.Label), cstr(m.Theme.Label), &limitLabels[0], &limitTexts[0], &values[0], &rgb[0], C.int(n))
}

// clearCapsules empties the capsule row. Main thread.
func clearCapsules(container unsafe.Pointer) { C.fd_capsules_clear(container) }

// setCapsuleRowVariant is a diagnostic stand's row for the capsules
// (standCapsuleRowEnv). TEMPORARY (T-056, v0.10.1).
func setCapsuleRowVariant(variant string) {
	c := C.CString(variant)
	defer C.free(unsafe.Pointer(c))
	C.fd_capsules_set_variant(c)
}

// capsuleEvents is where a press in the capsule row goes: effects.go hands it to
// the controller's capsuleAction. Until it is set a press does nothing.
var capsuleEvents = struct {
	sync.Mutex
	pressed func(action string)
}{}

func setCapsuleEvents(pressed func(action string)) {
	capsuleEvents.Lock()
	defer capsuleEvents.Unlock()
	capsuleEvents.pressed = pressed
}

//export fleetdeckCapsulePressed
func fleetdeckCapsulePressed(action *C.char) {
	capsuleEvents.Lock()
	pressed := capsuleEvents.pressed
	capsuleEvents.Unlock()
	if pressed != nil {
		pressed(C.GoString(action))
	}
}

// What capsules_darwin_test.go reads; Go test files cannot use cgo.

type capsulesProbe struct {
	segmentLabels    [2]string
	selectedSegment  int
	newCardTitle     string
	themeTitle       string
	levelValue       float64
	capsuleCount     int
	countAfterRedraw int
	rowPassesThrough bool
	presses          []string
	// clicksReach: a click at the middle of the tabs, the new card button and
	// the theme button, drawn on glass, reaches that control.
	clicksReach [3]bool
	// insideGlass: each of those controls, drawn on glass, is its glass's
	// content -- the one place a glass draws a view inside itself.
	insideGlass [3]bool
}

func probeCapsulesForTest(m capsuleModel) capsulesProbe {
	var out capsulesProbe
	window := C.fd_test_window(1512, 982)
	f := installFrame(window)
	f.layout(layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348}))
	f.setMode(glassModeGlass)
	setCapsuleEvents(func(action string) { out.presses = append(out.presses, action) })

	drawCapsules(f.capsules(), m, glassModeGlass)
	out.segmentLabels = [2]string{C.GoString(C.fd_test_segment_label(0)), C.GoString(C.fd_test_segment_label(1))}
	out.selectedSegment = int(C.fd_test_selected_segment())
	out.newCardTitle = C.GoString(C.fd_test_new_card_title())
	out.themeTitle = C.GoString(C.fd_test_theme_title())
	out.levelValue = float64(C.fd_test_level_value(0))
	out.capsuleCount = int(C.fd_test_capsule_count())
	out.rowPassesThrough = C.fd_test_row_passes_through(f.capsules()) != 0
	for which := range out.clicksReach {
		out.insideGlass[which] = C.fd_test_capsule_inside_glass(C.int(which)) != 0
		out.clicksReach[which] = C.fd_test_click_reaches_capsule(C.int(which)) != 0
	}
	C.fd_test_press_segment(1)
	C.fd_test_press_new_card()

	drawCapsules(f.capsules(), m, glassModeVibrancy)
	out.countAfterRedraw = int(C.fd_test_capsule_count()) + int(C.fd_test_subview_count(f.capsules())) - 1
	return out
}
