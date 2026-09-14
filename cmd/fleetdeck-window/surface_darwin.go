package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#include "frame_darwin.h"
#include "surface_darwin.h"
*/
import "C"

import (
	"sync"
	"unsafe"
)

// surface is the web view of a side surface -- the orchestrator's column or
// the sessions column -- inside its glass panel (surface_darwin.c). Every
// method is AppKit and runs on the main thread.
type surface struct {
	p    unsafe.Pointer
	kind string
	url  string
	// calls is the page's binding calls, answered in order (callQueue); nil
	// until glasswindow.go gives the surface one.
	calls *callQueue
}

// surfaceScripts is what a surface's pages get at the start of every document,
// in order: the host object with the bindings (hostscript.go), the page load
// report T-057 gives the board (owner.go), and the mark on a foreign build for
// the surface that shows the build (noticescript.go).
func surfaceScripts(kind, panelURL string, glass glassMode, b *bridge) []string {
	scripts := []string{hostScript(kind, glass, b.surfaceNames()), pageLoadScript(panelURL)}
	if notice := noticeScriptFor(kind); notice != "" {
		scripts = append(scripts, notice)
	}
	return scripts
}

// newSurface makes kind's web view in container, sharing board's process and
// store. It loads nothing: load does.
func newSurface(board, container unsafe.Pointer, kind, panelURL string, glass glassMode, b *bridge) *surface {
	scripts := surfaceScripts(kind, panelURL, glass, b)
	cScripts := make([]*C.char, len(scripts))
	for i, s := range scripts {
		cScripts[i] = C.CString(s)
	}
	name := C.CString(kind)
	defer func() {
		C.free(unsafe.Pointer(name))
		for _, s := range cScripts {
			C.free(unsafe.Pointer(s))
		}
	}()
	// A Go slice's backing array may be handed to C for the length of the call.
	p := C.fd_surface_create(board, container, name, &cScripts[0], C.int(len(cScripts)))
	return &surface{p: p, kind: kind}
}

func (s *surface) load(url string) {
	s.url = url
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))
	C.fd_surface_load(s.p, cURL)
}

// reload asks for the surface's page again at the address it was given: a page
// that never reached its document has nothing for WebKit's reload to repeat.
func (s *surface) reload() { s.load(s.url) }

func (s *surface) eval(js string) {
	cJS := C.CString(js)
	defer C.free(unsafe.Pointer(cJS))
	C.fd_surface_eval(s.p, cJS)
}

// send hands the page one of the window's messages (hostactions.js).
func (s *surface) send(message map[string]any) { s.eval(receiveScript(message)) }

func (s *surface) focus() { C.fd_surface_focus(s.p) }

// close takes the web view down and releases it; s is not used after.
func (s *surface) close() {
	if s.calls != nil {
		s.calls.close()
	}
	C.fd_surface_destroy(s.p)
	s.p = nil
}

// surfaceEvents is where the surfaces' web views report to: a binding call from
// a page, and a navigation to decide. effects.go sets both; until then a call is
// dropped and a navigation cancelled.
var surfaceEvents = struct {
	sync.Mutex
	message  func(surface, message string)
	navigate func(surface, target string) bool
}{}

func setSurfaceEvents(message func(surface, message string), navigate func(surface, target string) bool) {
	surfaceEvents.Lock()
	defer surfaceEvents.Unlock()
	surfaceEvents.message, surfaceEvents.navigate = message, navigate
}

//export fleetdeckSurfaceMessage
func fleetdeckSurfaceMessage(surface, message *C.char) {
	surfaceEvents.Lock()
	handle := surfaceEvents.message
	surfaceEvents.Unlock()
	if handle != nil {
		handle(C.GoString(surface), C.GoString(message))
	}
}

//export fleetdeckSurfaceNavigation
func fleetdeckSurfaceNavigation(surface, target *C.char) C.int {
	surfaceEvents.Lock()
	decide := surfaceEvents.navigate
	surfaceEvents.Unlock()
	if decide != nil && decide(C.GoString(surface), C.GoString(target)) {
		return 1
	}
	return 0
}

// What surface_darwin_test.go reads; Go test files cannot use cgo.

type surfaceProbe struct {
	clickInPanelLandsOn        string
	surfaceFrame               rect
	clickInPanelReachesSurface bool
	clickOnEdgeReachesStrip    bool
	drawsBackground            bool
	sharesPool                 bool
	sharesStore                bool
	userScripts                int
	liveAfterCreate            int
	liveAfterChurn             int
	subviewsAfterChurn         int
	classRegistrations         int
	eventsSet                  bool
	boardObserved              bool
	reportsNavigation          bool
}

func probeSurfacesForTest() surfaceProbe {
	var out surfaceProbe
	window := C.fd_test_board_window(1512, 982)
	f := installFrame(window)
	f.layout(layoutFor(1512, 982, panelWidths{Orchestrator: 368, Sessions: 348}))
	f.setMode(glassModeGlass)
	// The board moved under the frame is still the web view WebKit reports on:
	// installing the frame, as the window does, set the delegate on it.
	out.boardObserved = f.boardObserved()
	b := newBridge()
	setSurfaceEvents(func(string, string) {}, func(string, string) bool { return false })
	surfaceEvents.Lock()
	out.eventsSet = surfaceEvents.message != nil && surfaceEvents.navigate != nil
	surfaceEvents.Unlock()

	const page = "http://127.0.0.1:7777/?fleet=work"
	s := newSurface(f.board(), f.panelContent("sessions"), "sessions", page, glassModeGlass, b)
	webview := C.fd_test_surface_webview(s.p)
	out.drawsBackground = C.fd_test_draws_background(webview) != 0
	out.reportsNavigation = C.fd_test_reports_navigation(s.p) != 0
	out.sharesPool = C.fd_test_shares_pool(s.p, f.board()) != 0
	out.sharesStore = C.fd_test_shares_store(s.p, f.board()) != 0
	out.userScripts = int(C.fd_test_user_scripts(s.p))
	out.liveAfterCreate = int(C.fd_surface_live_handlers())
	// The sessions panel is at x 1156-1504: its middle is the surface's, its
	// edge the width strip's.
	out.clickInPanelReachesSurface = C.fd_test_hit_within(f.p, 1330, 491, webview) != 0
	out.clickInPanelLandsOn = C.GoString(C.fd_test_hit_chain(f.p, 1330, 491))
	r := C.fd_test_frame_of(webview)
	out.surfaceFrame = rect{X: float64(r.x), Y: float64(r.y), W: float64(r.w), H: float64(r.h)}
	out.clickOnEdgeReachesStrip = C.fd_test_hit_within(f.p, 1156, 491, C.fd_test_strip(f.p, 1)) != 0
	s.send(map[string]any{"type": "glass", "glass": "glass"})
	s.focus()
	s.load("about:blank")
	s.reload()
	s.close()

	// Twenty fleet switches' worth of surfaces made and taken down.
	for round := 0; round < 20; round++ {
		o := newSurface(f.board(), f.panelContent("orchestrator"), "orchestrator", page, glassModeGlass, b)
		t := newSurface(f.board(), f.panelContent("sessions"), "sessions", page, glassModeGlass, b)
		o.close()
		t.close()
	}
	out.liveAfterChurn = int(C.fd_surface_live_handlers())
	out.subviewsAfterChurn = int(C.fd_test_subview_count(f.panelContent("orchestrator"))) + int(C.fd_test_subview_count(f.panelContent("sessions")))
	out.classRegistrations = int(C.fd_surface_handler_class_registrations())
	return out
}
