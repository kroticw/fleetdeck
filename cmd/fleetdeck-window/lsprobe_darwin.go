//go:build darwin && lsprobe

package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
void lsprobeObserve(void);
void lsprobeAfterRunStarts(void);
int lsprobeRegistered(const char *bundle);
*/
import "C"

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// A build made to measure when a window started by exec has its bundle's path
// registered with LaunchServices (T-117, scripts/lsprobe). It is never shipped:
// only `go build -tags lsprobe` makes it, and only the lsprobe workflow does
// that, on a runner. At each mark of the start it stops the main thread to ask
// LaunchServices whether the bundle it runs from is registered, and writes the
// answer as one JSON line to the file FLEETDECK_LSPROBE_OUT names.
//
// Two answers per mark. The quick one, taken on the main thread at the mark,
// is NSWorkspace's list of the bundles under this bundle's identifier, which
// the lsprobe workflow makes unique to each run. The other is lsregister -dump,
// started at the mark beside the window, which goes on: one dump took 8 s on a
// macos-26 runner (scripts/updatecheck), and the start is not held that long.
// A dump's answer is about some moment between its start and its end.
//
// The marks: each start step (startupsteps.go) -- "started" right after exec
// and "the web view is made" once webview.New has returned; AppKit's
// will-finish and did-finish launching, which come inside webview.New, since
// it runs the application until it has finished launching; "the window runs",
// the first block the main queue runs once w.Run has started, where a
// handover's reregistering runs (handoverstart.go); and the first activation.
// A window started by exec on a runner may never be made active, so ten
// seconds into w.Run the probe activates it itself, and says so in the mark.

const lsprobeOutEnv = "FLEETDECK_LSPROBE_OUT"

// lsprobeRecord is one line the probe writes: a quick look at a mark (Kind
// "look"), or a dump started at one (Kind "dump").
type lsprobeRecord struct {
	Kind       string `json:"kind"`
	Mark       string `json:"mark"`
	UnixMs     int64  `json:"unix_ms"`
	SinceStart int64  `json:"since_start_ms"`
	EndUnixMs  int64  `json:"end_unix_ms"`
	Bundle     string `json:"bundle"`
	Registered bool   `json:"registered"`
	Err        string `json:"err,omitempty"`
}

var lsprobe struct {
	sync.Mutex
	out     *os.File
	bundle  string
	started time.Time
}

func init() {
	path := os.Getenv(lsprobeOutEnv)
	if path == "" {
		log.Fatalf("fleetdeck-window: a build made to measure the start, with no %s to write to", lsprobeOutEnv)
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("fleetdeck-window: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("fleetdeck-window: locate own binary: %v", err)
	}
	started, err := processStart()
	if err != nil {
		started = time.Now()
	}
	lsprobe.out, lsprobe.bundle, lsprobe.started = out, bundleOf(exe), started
	startupProbe = func(step string) {
		lsprobeMark(step)
		if step == "the web view is made" {
			C.lsprobeAfterRunStarts()
		}
	}
	C.lsprobeObserve()
}

//export lsprobeNotified
func lsprobeNotified(mark *C.char) {
	lsprobeMark(C.GoString(mark))
}

func lsprobeMark(mark string) {
	at := time.Now()
	bundle := C.CString(lsprobe.bundle)
	answer := C.lsprobeRegistered(bundle)
	C.free(unsafe.Pointer(bundle))
	look := lsprobeRecord{Kind: "look", Mark: mark, UnixMs: at.UnixMilli(), SinceStart: at.Sub(lsprobe.started).Milliseconds(),
		EndUnixMs: time.Now().UnixMilli(), Bundle: lsprobe.bundle, Registered: answer == 1}
	if answer < 0 {
		look.Err = "the bundle has no identifier"
	}
	lsprobeWrite(look)
	log.Printf("fleetdeck-window: start probe: %s, registered %v, %d ms into the process", mark, look.Registered, look.SinceStart)
	go func() {
		defer panics.in("in the start probe's dump").guard()
		registered, err := supervisor.LaunchServices{Lsregister: supervisor.LsregisterPath}.Registered(lsprobe.bundle)
		dump := lsprobeRecord{Kind: "dump", Mark: mark, UnixMs: look.UnixMs, SinceStart: look.SinceStart,
			EndUnixMs: time.Now().UnixMilli(), Bundle: lsprobe.bundle, Registered: registered}
		if err != nil {
			dump.Err = err.Error()
		}
		lsprobeWrite(dump)
	}()
}

func lsprobeWrite(r lsprobeRecord) {
	line, _ := json.Marshal(r)
	lsprobe.Lock()
	defer lsprobe.Unlock()
	if _, err := lsprobe.out.Write(append(line, '\n')); err != nil {
		log.Printf("fleetdeck-window: the start probe could not write its mark %q: %v", r.Mark, err)
	}
}
