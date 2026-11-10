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
	"runtime"
	"sync"
	"unsafe"
)

// drawCapsules draws m into the frame's capsule row (capsules_darwin.c), in the
// panels' material, and answers the row's minimum width: its narrowest form.
// Main thread.
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
	flag := func(b bool) C.int {
		if b {
			return 1
		}
		return 0
	}

	// Each slice one longer than its items, so &x[0] is valid with none.
	fleetNames := make([]*C.char, len(m.Fleet.Fleets)+1)
	fleetWaiting := make([]C.int, len(m.Fleet.Fleets)+1)
	fleetCurrent := make([]C.int, len(m.Fleet.Fleets)+1)
	for i, f := range m.Fleet.Fleets {
		fleetNames[i], fleetWaiting[i], fleetCurrent[i] = cstr(f.Name), C.int(f.Waiting), flag(f.Current)
	}
	limitRGB := make([]C.double, 3*len(m.Limits)+3)
	for i, limit := range m.Limits {
		c := rgbOf(limit.Color)
		copy(limitRGB[3*i:], c[:])
	}
	counterTexts := make([]*C.char, len(m.Counters)+1)
	counterRGB := make([]C.double, 3*len(m.Counters)+3)
	counterHollow := make([]C.int, len(m.Counters)+1)
	for i, pill := range m.Counters {
		counterTexts[i], counterHollow[i] = cstr(pill.Text), flag(pill.Hollow)
		c := rgbOf(pill.Color)
		copy(counterRGB[3*i:], c[:])
	}
	busy := m.busiestLimit()
	busyRGB, accentRGB := rgbOf(busy.Color), rgbOf(m.Accent)
	var update capsuleUpdate
	if m.Update != nil {
		update = *m.Update
	}
	bar := C.fd_bar{
		mode:          cstr(string(mode)),
		brandName:     cstr(m.Brand.Name),
		brandVersion:  cstr(m.Brand.Version),
		fleetName:     cstr(m.Fleet.Name),
		fleetLabel:    cstr(m.Fleet.Label),
		fleetAll:      cstr(m.Fleet.All),
		fleetAdd:      cstr(m.Fleet.Add),
		fleetNames:    &fleetNames[0],
		fleetWaiting:  &fleetWaiting[0],
		fleetCurrent:  &fleetCurrent[0],
		fleetCount:    C.int(len(m.Fleet.Fleets)),
		limitsLabel:   cstr(m.LimitsLabel),
		busyName:      cstr(busy.Name),
		busyText:      cstr(busy.Text),
		limitsTooltip: cstr(busy.Tooltip),
		busyRGB:       &busyRGB[0],
		limitRGB:      &limitRGB[0],
		limitCount:    C.int(len(m.Limits)),
		updateText:    cstr(update.Text),
		updateAction:  cstr(update.Action),
		counterTexts:  &counterTexts[0],
		counterRGB:    &counterRGB[0],
		counterHollow: &counterHollow[0],
		counterCount:  C.int(len(m.Counters)),
		themeLabel:    cstr(m.Theme.Label),
		themeLabels:   [3]*C.char{cstr(m.Theme.Light), cstr(m.Theme.System), cstr(m.Theme.Dark)},
		themeSegment:  C.int(m.Theme.segment()),
		accentRGB:     &accentRGB[0],
		frameMinWidth: C.double(frameMinWidth()),
	}
	// bar is Go memory holding pointers into Go slices: cgo takes it only with
	// those slices pinned.
	var pins runtime.Pinner
	defer pins.Unpin()
	for _, p := range []any{&fleetNames[0], &fleetWaiting[0], &fleetCurrent[0], &limitRGB[0], &counterTexts[0], &counterRGB[0], &counterHollow[0], &busyRGB[0], &accentRGB[0]} {
		pins.Pin(p)
	}
	return float64(C.fd_capsules_draw(container, &bar))
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

type menuItemProbe struct {
	title     string
	checked   bool
	separator bool
	waiting   int
}

type capsulesProbe struct {
	texts         map[string]string
	greens        map[string]float64
	counterHollow []bool
	brandIcon     bool
	slots         []string
	// Each slot's y and height in the row.
	slotY, slotH []float64
	// The theme's segments: their number, the selected one, their tooltips and
	// whether each is an image; the control's border shape (-1 before macOS 26).
	themeSegments, themeSelected int
	themeTooltips                [3]string
	themeImages                  [3]bool
	borderShape                  int
	selectedTopInset             float64
	roundedTopInset              float64
	// The fleet's menu as a press opened it, and where: its top left and the
	// fleet capsule's left and bottom, in the window's content from its top left.
	menu                     []menuItemProbe
	menuX, menuY             float64
	fleetX, fleetBottom      float64
	capsuleCount             int
	countAfterRedraw         int
	rowPassesThrough         bool
	presses                  []string
	clicksReach, insideGlass map[string]bool
	// measured: the frame as a stand measures it (standframe_darwin.go).
	measured standFrameReport
}

func probeCapsuleCountForTest(m capsuleModel) int {
	window := C.fd_test_window(1512, 982)
	f := installFrame(window)
	f.layout(layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348}))
	f.setMode(glassModeGlass)
	drawCapsules(f.capsules(), m, glassModeGlass)
	return int(C.fd_test_capsule_count())
}

func testText(what string) string {
	c := C.CString(what)
	defer C.free(unsafe.Pointer(c))
	return C.GoString(C.fd_test_text(c))
}

func testGreen(what string) float64 {
	c := C.CString(what)
	defer C.free(unsafe.Pointer(c))
	return float64(C.fd_test_green(c))
}

func testPress(what string) {
	c := C.CString(what)
	defer C.free(unsafe.Pointer(c))
	C.fd_test_press(c)
}

func testReach(what string) (reach, inside bool) {
	c := C.CString(what)
	defer C.free(unsafe.Pointer(c))
	return C.fd_test_click_reaches(c) != 0, C.fd_test_inside_glass(c) != 0
}

func probeCapsulesForTest(m capsuleModel) capsulesProbe {
	out := capsulesProbe{texts: map[string]string{}, greens: map[string]float64{}, clicksReach: map[string]bool{}, insideGlass: map[string]bool{}}
	window := C.fd_test_window(1512, 982)
	f := installFrame(window)
	f.layout(layoutPastButtons(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348}, 79))
	f.setMode(glassModeGlass)
	setCapsuleEvents(func(action string) { out.presses = append(out.presses, action) })
	defer setCapsuleEvents(nil)

	drawCapsules(f.capsules(), m, glassModeGlass)
	for _, what := range []string{"brandName", "brandVersion", "fleetName", "fleetLabel", "limitsLabel", "busyName", "busyText", "limitsTooltip", "updateText", "updateAction", "counter0", "counter1"} {
		out.texts[what] = testText(what)
	}
	for _, what := range []string{"fleetDot", "busyText", "dot0", "dot1", "counter0", "counterTint0"} {
		out.greens[what] = testGreen(what)
	}
	for i := 0; i < len(m.Counters); i++ {
		out.counterHollow = append(out.counterHollow, C.fd_test_counter_hollow(C.int(i)) != 0)
	}
	out.brandIcon = C.fd_test_brand_has_icon() != 0
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		out.slots = append(out.slots, C.GoString(C.fd_test_capsule_slot_name(C.int(i))))
		out.slotY = append(out.slotY, float64(C.fd_test_capsule_slot_y(C.int(i))))
		out.slotH = append(out.slotH, float64(C.fd_test_capsule_slot_h(C.int(i))))
	}
	out.themeSegments = int(C.fd_test_theme_segments())
	out.themeSelected = int(C.fd_test_theme_selected())
	for i := range out.themeTooltips {
		out.themeTooltips[i] = C.GoString(C.fd_test_theme_tooltip(C.int(i)))
		out.themeImages[i] = C.fd_test_theme_segment_is_image(C.int(i)) != 0
	}
	out.borderShape = int(C.fd_test_segment_border_shape())
	out.selectedTopInset = float64(C.fd_test_selected_segment_top_inset())
	out.roundedTopInset = float64(C.fd_test_rounded_segment_top_inset())
	out.capsuleCount = int(C.fd_test_capsule_count())
	out.rowPassesThrough = C.fd_test_row_passes_through(f.capsules()) != 0
	for _, what := range []string{"fleet", "limits", "update", "theme"} {
		out.clicksReach[what], out.insideGlass[what] = testReach(what)
	}
	out.measured = measureFrame(f, window, glassModeGlass)

	C.fd_test_capture_menus(1)
	testPress("fleet")
	C.fd_test_capture_menus(0)
	for i := 0; i < int(C.fd_test_menu_items()); i++ {
		out.menu = append(out.menu, menuItemProbe{
			title:     C.GoString(C.fd_test_menu_item_title(C.int(i))),
			checked:   C.fd_test_menu_item_state(C.int(i)) == 1,
			separator: C.fd_test_menu_item_separator(C.int(i)) != 0,
			waiting:   int(C.fd_test_menu_item_waiting(C.int(i))),
		})
	}
	out.menuX, out.menuY = float64(C.fd_test_menu_x()), float64(C.fd_test_menu_y())
	fleet := C.fd_test_fleet_in_window()
	out.fleetX, out.fleetBottom = float64(fleet.x), float64(C.fd_test_fleet_bottom_in_window())
	// The second fleet, All, Add; the limits, the update, the dark theme.
	for _, i := range []int{1, len(out.menu) - 2, len(out.menu) - 1} {
		C.fd_test_menu_pick(C.int(i))
	}
	testPress("limits")
	testPress("update")
	C.fd_test_press_theme(2)

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
	out.geometry = layoutFor(out.laidWidth, height, widths)
	f.layout(out.geometry)

	out.rowWidth = float64(C.fd_test_row_width())
	out.minContentWidth = float64(C.fd_test_min_content_width(f.capsules()))
	for i := 0; i < int(C.fd_test_capsule_slots()); i++ {
		fr := C.fd_test_capsule_slot_frame(C.int(i))
		out.capsules = append(out.capsules, drawnCapsule{
			name: C.GoString(C.fd_test_capsule_slot_name(C.int(i))), x: float64(fr.x), w: float64(fr.w), visible: fr.visible != 0,
		})
	}
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
	drawCapsules(f.capsules(), m, glassModeGlass)
	f.layout(layoutFor(from, height, widths))
	C.fd_test_layout_window(window)
	f.layout(layoutFor(to, height, widths))
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
