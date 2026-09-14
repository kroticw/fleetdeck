//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include <stdint.h>
#include "nav_darwin.h"
*/
import "C"

import "fmt"

// What WKWebView says of a navigation (nav_darwin.c). The page's own word
// (pageLoadScript) begins only once its document has, so it cannot tell a
// navigation that is under way from one that never started or has failed
// before any document arrived; these can.
type navKind int

const (
	navStarted navKind = iota + 1
	navCommitted
	navFinished
	navFailedProvisional
	navFailed
	navProcessGone
)

func (k navKind) String() string {
	switch k {
	case navStarted:
		return "started"
	case navCommitted:
		return "committed"
	case navFinished:
		return "finished"
	case navFailedProvisional:
		return "failed before it committed"
	case navFailed:
		return "failed after it committed"
	case navProcessGone:
		return "lost its web content process"
	}
	return fmt.Sprintf("navKind(%d)", int(k))
}

// navEvent is one thing WKWebView said of a navigation.
type navEvent struct {
	kind navKind
	// id tells one navigation from another: the WKNavigation's address, 0 for
	// an event about the web view rather than a navigation.
	id   uintptr
	href string
	// errCode and errDomain are the NSError's, for a failure.
	errCode   int
	errDomain string
	// webProcess is the web content process's PID, 0 when WKWebView does not say.
	webProcess int
}

func (e navEvent) String() string {
	s := fmt.Sprintf("%s (navigation %#x, %s, web content pid %d)", e.kind, e.id, e.href, e.webProcess)
	if e.errDomain != "" {
		s += fmt.Sprintf(": %s %d", e.errDomain, e.errCode)
	}
	return s
}

// navigationObserver receives every event of the board's web view, and
// surfaceNavigationObserver every event of a side surface's, with its name, on
// the UI thread: WKWebView calls its delegate there.
var (
	navigationObserver        func(navEvent)
	surfaceNavigationObserver func(surface string, e navEvent)
)

//export fleetdeckNavigationEvent
func fleetdeckNavigationEvent(webView *C.char, kind C.int, id C.uintptr_t, href *C.char, code C.long, domain *C.char, pid C.int) {
	e := navEvent{
		kind:       navKind(kind),
		id:         uintptr(id),
		href:       C.GoString(href),
		errCode:    int(code),
		errDomain:  C.GoString(domain),
		webProcess: int(pid),
	}
	if surface := C.GoString(webView); surface != "" {
		if surfaceNavigationObserver != nil {
			surfaceNavigationObserver(surface, e)
		}
		return
	}
	if navigationObserver != nil {
		navigationObserver(e)
	}
}

// observeBoardNavigation has f told of every navigation event of the board.
// The delegate itself is set when the frame goes in (installFrame,
// frame.boardObserved), on the board's web view: the one place that knows it.
func observeBoardNavigation(f func(navEvent)) {
	navigationObserver = f
}

// observeSurfaceNavigation has f told of every navigation event of the side
// surfaces' web views, whose delegates report from the start
// (surface_darwin.c).
func observeSurfaceNavigation(f func(surface string, e navEvent)) {
	surfaceNavigationObserver = f
}
