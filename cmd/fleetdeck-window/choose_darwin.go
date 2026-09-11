package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "choose_darwin.h"
*/
import "C"
import "unsafe"

// chooseFolderBindingName is the function the window gives the setup page
// (web/js/setup.js): it shows the system's folder chooser and answers the
// chosen folder's path, or "" when the person cancelled. In a browser nothing
// binds it, and the page offers only its text field. The name is a contract
// across two languages; choose_test.go checks both sides spell it the same.
const chooseFolderBindingName = "fleetdeckChooseFolder"

// chooseFolder is what the page's call runs. It blocks in the chooser's modal
// loop on the main thread, which is where the web view calls a bound function
// and where AppKit wants the panel run; the window's own events wait until
// the chooser is closed.
func chooseFolder(message, prompt string) string {
	cMessage := C.CString(message)
	defer C.free(unsafe.Pointer(cMessage))
	cPrompt := C.CString(prompt)
	defer C.free(unsafe.Pointer(cPrompt))

	chosen := C.fleetdeck_choose_folder(cMessage, cPrompt)
	if chosen == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(chosen))
	return C.GoString(chosen)
}
