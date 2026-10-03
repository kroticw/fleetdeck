package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
int lsprobeRegistered(const char *bundle);
*/
import "C"

import (
	"errors"
	"unsafe"
)

// quickLook says whether LaunchServices lists bundle under its own bundle
// identifier, which each scenario makes unique to it: milliseconds, where a
// dump takes seconds.
func quickLook(bundle string) (bool, error) {
	path := C.CString(bundle)
	defer C.free(unsafe.Pointer(path))
	switch C.lsprobeRegistered(path) {
	case 1:
		return true, nil
	case 0:
		return false, nil
	default:
		return false, errors.New("the bundle has no identifier")
	}
}
