package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
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
//
// name is the app's name as its menu says it: the app menu, and its Quit
// item -- "fleetdeck dev" for a dev app, so the two apps' menus differ.
func installMenu(name string) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	C.fleetdeck_install_menu(cname)
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

// windowFullScreenOptionsForTest asks the installed window delegate for the
// presentation options of full screen, AppKit proposing proposed; -1 when the
// delegate does not answer.
func windowFullScreenOptionsForTest(window unsafe.Pointer, proposed uint) int {
	return int(C.fleetdeck_window_full_screen_options_for_test(window, C.ulong(proposed)))
}
