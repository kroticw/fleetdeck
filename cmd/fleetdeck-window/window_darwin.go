package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "window_darwin.h"
*/
import "C"

import (
	"log"
	"sync"
	"unsafe"
)

// standAppearance is a stand's appearance (standAppearanceEnv), "light" or
// "dark" in place of the system's; "" off a stand.
var standAppearance string

// applyAppearance sets the window's light or dark look to the page's theme:
// "light", "dark", or "auto" for the system's -- a stand's, on a stand. The
// log says what the app is drawn in after it, as AppKit reports it: a
// screenshot said to be dark is taken on that word, not on what was asked.
func applyAppearance(choice string) {
	asked := choice
	if choice != "light" && choice != "dark" && standAppearance != "" {
		choice = standAppearance
	}
	c := C.CString(choice)
	defer C.free(unsafe.Pointer(c))
	C.fd_window_set_appearance(c)
	log.Printf("fleetdeck-window: the window is drawn in %s (theme %q)", C.GoString(C.fd_window_effective_appearance()), asked)
}

func openExternalURL(url string) {
	c := C.CString(url)
	defer C.free(unsafe.Pointer(c))
	C.fd_open_external(c)
}

func focusView(view unsafe.Pointer) { C.fd_focus_view(view) }

// The panels' widths and folds, kept by the window: in the window the page has
// no shared <main> to take percentages of (spec 6.4).
const (
	orchestratorWidthKey  = "glassOrchestratorWidth"
	sessionsWidthKey      = "glassSessionsWidth"
	orchestratorFoldedKey = "glassOrchestratorFolded"
	sessionsFoldedKey     = "glassSessionsFolded"
	defaultOrchestrator   = 368.0
	defaultSessions       = 348.0
)

func defaultsDouble(key string, fallback float64) float64 {
	c := C.CString(key)
	defer C.free(unsafe.Pointer(c))
	var found C.int
	v := float64(C.fd_defaults_double(c, &found))
	if found == 0 || v <= 0 {
		return fallback
	}
	return v
}

func defaultsBool(key string) bool {
	c := C.CString(key)
	defer C.free(unsafe.Pointer(c))
	return C.fd_defaults_bool(c) != 0
}

// useWidthsSuite keeps the panels' widths in suite from now on (widthsSuite);
// "" leaves them in the app's own defaults.
func useWidthsSuite(suite string) {
	if suite == "" {
		return
	}
	c := C.CString(suite)
	defer C.free(unsafe.Pointer(c))
	C.fd_defaults_use_suite(c)
}

func loadPanelWidths() panelWidths {
	return panelWidths{
		Orchestrator:       defaultsDouble(orchestratorWidthKey, defaultOrchestrator),
		Sessions:           defaultsDouble(sessionsWidthKey, defaultSessions),
		OrchestratorFolded: defaultsBool(orchestratorFoldedKey),
		SessionsFolded:     defaultsBool(sessionsFoldedKey),
	}
}

func storePanelWidths(w panelWidths) {
	for key, v := range map[string]float64{orchestratorWidthKey: w.Orchestrator, sessionsWidthKey: w.Sessions} {
		c := C.CString(key)
		C.fd_defaults_set_double(c, C.double(v))
		C.free(unsafe.Pointer(c))
	}
	for key, v := range map[string]bool{orchestratorFoldedKey: w.OrchestratorFolded, sessionsFoldedKey: w.SessionsFolded} {
		c := C.CString(key)
		folded := C.int(0)
		if v {
			folded = 1
		}
		C.fd_defaults_set_bool(c, folded)
		C.free(unsafe.Pointer(c))
	}
}

func observeWindow(window unsafe.Pointer) { C.fd_observe_window(window) }

func windowContentSize(window unsafe.Pointer) (width, height float64) {
	var w, h C.double
	C.fd_window_content_size(window, &w, &h)
	return float64(w), float64(h)
}

func windowIsFullscreen(window unsafe.Pointer) bool { return C.fd_window_is_fullscreen(window) != 0 }

// windowEvents is where word of the window changing goes: glasswindow.go.
var windowEvents = struct {
	sync.Mutex
	changed func(kind string)
}{}

func setWindowEvents(changed func(kind string)) {
	windowEvents.Lock()
	defer windowEvents.Unlock()
	windowEvents.changed = changed
}

//export fleetdeckWindowChanged
func fleetdeckWindowChanged(kind *C.char) {
	windowEvents.Lock()
	changed := windowEvents.changed
	windowEvents.Unlock()
	if changed != nil {
		changed(C.GoString(kind))
	}
}
