package main

// What panicexit_test.go needs of the main dispatch queue, kept out of the
// test file because a test file cannot use cgo (golang/go#4030; see
// testsupport_darwin.go). Like that file's functions, these ship in the binary
// and are never called from main().

/*
#cgo LDFLAGS: -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <dispatch/dispatch.h>

extern void fleetdeckTestPanicInBlock(void);
extern void fleetdeckTestBlockRan(void);

static void t_panicBlock(void *ctx) { fleetdeckTestPanicInBlock(); }
static void t_markBlock(void *ctx) { fleetdeckTestBlockRan(); }

static void t_postPanicBlock(void) { dispatch_async_f(dispatch_get_main_queue(), NULL, t_panicBlock); }
static void t_postMarkBlock(void) { dispatch_async_f(dispatch_get_main_queue(), NULL, t_markBlock); }
static void t_runMainLoop(double seconds) { CFRunLoopRunInMode(kCFRunLoopDefaultMode, seconds, false); }
*/
import "C"

// testPanicValue is what the block on the main queue panics with.
const testPanicValue = "a panic inside a block on the main dispatch queue"

// testBlockRan is set by the block testWaitLikeDestroy posts.
var testBlockRan bool

//export fleetdeckTestPanicInBlock
func fleetdeckTestPanicInBlock() { panic(testPanicValue) }

//export fleetdeckTestBlockRan
func fleetdeckTestBlockRan() { testBlockRan = true }

// testPostPanicBlock puts a block on the main dispatch queue that panics once
// the run loop runs it, as a w.Dispatch callback would.
func testPostPanicBlock() { C.t_postPanicBlock() }

// testRunMainLoop turns the main run loop for up to seconds.
func testRunMainLoop(seconds float64) { C.t_runMainLoop(C.double(seconds)) }

// testWaitLikeDestroy is what webview's destructor does before it returns
// (webview.h, deplete_run_loop_event_queue): a block posted to the main queue,
// and the run loop turned until it has run. Called while the main queue is
// still inside another block, it never returns.
func testWaitLikeDestroy() {
	C.t_postMarkBlock()
	for !testBlockRan {
		C.t_runMainLoop(0.05)
	}
}
