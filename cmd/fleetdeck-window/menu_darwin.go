package main

/*
#cgo LDFLAGS: -framework Cocoa
#include "menu_darwin.h"
*/
import "C"

import (
	"sync"
	"unsafe"
)

// menuReload is what the menu's Reload does: glasswindow.go sets it to reload
// every web view. Until it is set, Reload does nothing.
var menuReload = struct {
	sync.Mutex
	run func()
}{}

func setMenuReload(run func()) {
	menuReload.Lock()
	defer menuReload.Unlock()
	menuReload.run = run
}

//export fleetdeckMenuReload
func fleetdeckMenuReload() {
	menuReload.Lock()
	run := menuReload.run
	menuReload.Unlock()
	if run != nil {
		run()
	}
}

// installMenu builds the app's menu bar. Call it any time after
// webview.New() returns -- by then the app has already finished launching
// (see main.go's package doc for why that is already true, not assumed).
func installMenu() {
	C.fleetdeck_install_menu()
}

// installCloseToHide makes closing window hide it instead of tearing the
// engine down, and makes a Dock icon click bring it back. window must be
// the pointer webview's Window() returns.
func installCloseToHide(window unsafe.Pointer) {
	C.fleetdeck_install_close_to_hide(window)
}

// windowShouldCloseForTest drives the installed window delegate's
// windowShouldClose: exactly as AppKit would, without any user interaction.
// Exists only so menu_darwin_test.go has something to call: Go test files
// cannot themselves use cgo (see menu_darwin_testsupport.go's package doc).
func windowShouldCloseForTest(window unsafe.Pointer) int {
	return int(C.fleetdeck_window_should_close_for_test(window))
}
