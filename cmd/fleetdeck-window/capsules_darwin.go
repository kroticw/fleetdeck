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
	"fmt"
	"sync"
	"unsafe"
)

// drawCapsules draws m into the frame's capsule row (capsules_darwin.c), in the
// panels' material, and answers the row's minimum width: its narrowest form,
// which the frame keeps room for (layoutWithRow). Main thread.
func drawCapsules(container unsafe.Pointer, m capsuleModel, mode glassMode) float64 {
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
	rgbOf := func(hex string) [3]C.double {
		if r, g, b, ok := hexColor(hex); ok {
			return [3]C.double{C.double(float64(r) / 255), C.double(float64(g) / 255), C.double(float64(b) / 255)}
		}
		return [3]C.double{-1, 0, 0}
	}

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
		c := rgbOf(limit.Color)
		copy(rgb[3*i:], c[:])
	}
	compact := m.compactLimits()
	compactRGB := rgbOf(compact.Color)
	return float64(C.fd_capsules_draw(container, cstr(string(mode)), &tabIDs[0], &tabLabels[0], C.int(len(m.Tabs)),
		C.int(selected), cstr(m.NewCard.Label), cstr(m.Theme.Label), &limitLabels[0], &limitTexts[0], &values[0], &rgb[0],
		C.int(n), cstr(compact.Text), cstr(compact.Tooltip), &compactRGB[0], C.double(frameMinWidth())))
}

// clearCapsules empties the capsule row. Main thread.
func clearCapsules(container unsafe.Pointer) { C.fd_capsules_clear(container) }

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

type drawnCapsule struct {
	name    string
	x, w    float64
	visible bool
}

// capsuleLayoutProbe is the row in a window width wide, its panels both
// unfolded or both folded, laid out as glasswindow.go lays it out: the frame,
// the capsules drawn, the frame again with the row's minimum. drawnAt, when
// not 0, is the width the capsules were drawn at before the window was resized
// to width, the capsules not drawn again.
//
// contentWidth is the window's content width once the capsules are drawn,
// grown to the window's minimum; laidWidth the width the frame is laid out for.
type capsuleLayoutProbe struct {
	width, drawnAt  float64
	folded          bool
	contentWidth    float64
	laidWidth       float64
	geometry        geometry
	rowWidth        float64
	rowMin          float64
	minContentWidth float64
	minAfterClear   float64
	capsules        []drawnCapsule

	compactText, compactTooltip                           string
	themeIconTitle, themeIconTooltip, themeIconAccessible string
	iconPresses                                           []string
}

func probeCapsuleLayoutForTest(m capsuleModel, width, drawnAt float64, folded bool) capsuleLayoutProbe {
	out := capsuleLayoutProbe{width: width, drawnAt: drawnAt, folded: folded}
	const height = 700
	widths := panelWidths{Orchestrator: 368, Sessions: 348, OrchestratorFolded: folded, SessionsFolded: folded}
	first := width
	if drawnAt != 0 {
		first = drawnAt
	}
	window := C.fd_test_window(C.double(first), height)
	f := installFrame(window)
	f.setMode(glassModeGlass)
	f.layout(layoutFor(first, height, widths))
	out.rowMin = drawCapsules(f.capsules(), m, glassModeGlass)
	// A window grown by the draw is laid out again at its new width, as
	// glasswindow.go does on the window's resize.
	out.contentWidth, _ = windowContentSize(window)
	out.laidWidth = width
	if drawnAt == 0 {
		out.laidWidth = out.contentWidth
	}
	out.geometry = layoutWithRow(out.laidWidth, height, widths, out.rowMin)
	f.layout(out.geometry)

	out.rowWidth = float64(C.fd_test_row_width())
	out.minContentWidth = float64(C.fd_test_min_content_width(f.capsules()))
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		fr := C.fd_test_capsule_slot_frame(C.int(i))
		out.capsules = append(out.capsules, drawnCapsule{
			name: C.GoString(C.fd_test_capsule_slot_name(C.int(i))), x: float64(fr.x), w: float64(fr.w), visible: fr.visible != 0,
		})
	}
	out.compactText = C.GoString(C.fd_test_compact_text())
	out.compactTooltip = C.GoString(C.fd_test_compact_tooltip())
	out.themeIconTitle = C.GoString(C.fd_test_theme_icon_title())
	out.themeIconTooltip = C.GoString(C.fd_test_theme_icon_tooltip())
	out.themeIconAccessible = C.GoString(C.fd_test_theme_icon_accessibility_label())
	setCapsuleEvents(func(action string) { out.iconPresses = append(out.iconPresses, action) })
	C.fd_test_press_theme_icon()
	setCapsuleEvents(nil)

	clearCapsules(f.capsules())
	out.minAfterClear = float64(C.fd_test_min_content_width(f.capsules()))
	return out
}

// capsuleRegrowProbe is the row drawn in a window from wide, both panels
// unfolded, and the frame then laid out for a window to wide, the capsules not
// drawn again: what entering or leaving full screen does. Read after the layout
// pass AppKit runs before the window's next frame on screen.
type capsuleRegrowProbe struct {
	from, to float64
	rowWidth float64
	capsules []drawnCapsule
}

func (p capsuleRegrowProbe) String() string {
	return fmt.Sprintf("drawn at %v, laid out at %v, row %v", p.from, p.to, p.rowWidth)
}

func probeCapsuleRegrowForTest(m capsuleModel, from, to float64) capsuleRegrowProbe {
	out := capsuleRegrowProbe{from: from, to: to}
	const height = 700
	widths := panelWidths{Orchestrator: 368, Sessions: 348}
	window := C.fd_test_window(C.double(from), height)
	f := installFrame(window)
	f.setMode(glassModeGlass)
	f.layout(layoutFor(from, height, widths))
	rowMin := drawCapsules(f.capsules(), m, glassModeGlass)
	f.layout(layoutWithRow(from, height, widths, rowMin))
	C.fd_test_layout_window(window)
	f.layout(layoutWithRow(to, height, widths, rowMin))
	C.fd_test_layout_window(window)
	out.rowWidth = float64(C.fd_test_row_width())
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		fr := C.fd_test_capsule_slot_frame(C.int(i))
		out.capsules = append(out.capsules, drawnCapsule{
			name: C.GoString(C.fd_test_capsule_slot_name(C.int(i))), x: float64(fr.x), w: float64(fr.w), visible: fr.visible != 0,
		})
	}
	clearCapsules(f.capsules())
	return out
}

// capsuleThemeProbe is the capsules drawn with the app dark, then light: the
// appearance each capsule's content has of its own on glass, as
// fd_test_capsule_slot_appearance names it, and how light each opaque capsule's
// background is.
type capsuleThemeProbe struct {
	glass                                bool
	onGlassInDarkApp                     []string
	opaqueInDarkApp, opaqueInLightApp    []float64
	appAppearanceBefore, appAppearanceAt string
}

func capsuleContentAppearances() []string {
	var out []string
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		out = append(out, C.GoString(C.fd_test_capsule_slot_appearance(C.int(i))))
	}
	return out
}

func capsuleBackgroundBrightness() []float64 {
	var out []float64
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		out = append(out, float64(C.fd_test_capsule_slot_background_brightness(C.int(i))))
	}
	return out
}

func setAppAppearanceForTest(name string) {
	c := C.CString(name)
	defer C.free(unsafe.Pointer(c))
	C.fd_test_set_app_appearance(c)
}

func probeCapsuleThemeForTest(m capsuleModel) capsuleThemeProbe {
	out := capsuleThemeProbe{glass: C.fd_glass_available() != 0}
	window := C.fd_test_window(1512, 982)
	f := installFrame(window)
	f.layout(layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348}))
	out.appAppearanceBefore = C.GoString(C.fd_test_app_appearance())

	setAppAppearanceForTest("NSAppearanceNameDarkAqua")
	f.setMode(glassModeGlass)
	drawCapsules(f.capsules(), m, glassModeGlass)
	out.onGlassInDarkApp = capsuleContentAppearances()
	f.setMode(glassModeOpaque)
	drawCapsules(f.capsules(), m, glassModeOpaque)
	out.opaqueInDarkApp = capsuleBackgroundBrightness()

	setAppAppearanceForTest("NSAppearanceNameAqua")
	drawCapsules(f.capsules(), m, glassModeOpaque)
	out.opaqueInLightApp = capsuleBackgroundBrightness()

	setAppAppearanceForTest(out.appAppearanceBefore)
	clearCapsules(f.capsules())
	out.appAppearanceAt = C.GoString(C.fd_test_app_appearance())
	return out
}
