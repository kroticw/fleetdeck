//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>
#include <unistd.h>

typedef struct {
	int access;
	double askX, askY, atX, atY;
} sp_result;

// sp_move posts mouse moves to the top middle of the main display, or its
// middle, and reads where the pointer is after.
static sp_result sp_move(int top) {
	sp_result r = {0};
	r.access = CGPreflightPostEventAccess();
	CGRect b = CGDisplayBounds(CGMainDisplayID());
	r.askX = b.origin.x + b.size.width / 2;
	r.askY = top ? b.origin.y : b.origin.y + b.size.height / 2;
	// From a little below, then onto the point: the edge is crossed, as a hand
	// crosses it.
	double from[2] = {top ? r.askY + 40 : r.askY - 40, r.askY};
	for (int i = 0; i < 2; i++) {
		CGEventRef e = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, CGPointMake(r.askX, from[i]), kCGMouseButtonLeft);
		if (e) {
			CGEventPost(kCGHIDEventTap, e);
			CFRelease(e);
		}
		usleep(150000);
	}
	CGEventRef now = CGEventCreate(NULL);
	if (now) {
		CGPoint p = CGEventGetLocation(now);
		r.atX = p.x;
		r.atY = p.y;
		CFRelease(now);
	}
	return r;
}
*/
import "C"

func movePointer(top bool) (result, error) {
	t := C.int(0)
	if top {
		t = 1
	}
	r := C.sp_move(t)
	return result{access: r.access != 0, askX: float64(r.askX), askY: float64(r.askY), atX: float64(r.atX), atY: float64(r.atY)}, nil
}
