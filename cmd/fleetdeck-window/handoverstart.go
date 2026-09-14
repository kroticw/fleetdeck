//go:build darwin

package main

import (
	"context"
	"log"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// A window started by an update takes the panel over before anything of its
// window is made (T-056, v0.10.1). The window that started it gives the whole
// handover, to done, 2436 ms from its launch when it is v0.10.0, and making the
// window took more than that on a cold first start: on macOS 26 (run
// 34861301605) the web view was made 1575 ms after the process started, and the
// glass frame was not made by 2456 ms, when the v0.10.0 window gave up. The
// takeover used to begin only after the frame, the bindings and codesign.
//
// So the takeover runs in a goroutine of its own from main's first moments,
// while webview.New and the rest of AppKit stay on the main thread, as AppKit
// requires. Nothing of the takeover up to done waits for the window. What the
// handover comes to for the window waits for it, in gate: the panel's page
// asked for at done, and the window closed at a failure. A failure before the
// window is made ends the process there, with no window shown: the window that
// started this one says why, and starts its panel again.
//
// LaunchServices: this window starts by exec from the staged path, and that
// path is registered by the time the window is ready; when in the process's
// start it is registered is not measured (docs/engineering/window-and-panel.md,
// on taking the panel over before the window is made). With the takeover ahead of the window, the check-in may come
// after the takeover has had LaunchServices forget that path, and put it back.
// So once the window runs -- its check-in behind it, whether that is made when
// the application object is created or when it finishes launching -- a
// takeover that has swapped the bundles has LaunchServices forget the staged
// path and take the canonical one again. A swap made after that point is
// followed by the takeover's own reregistering, which then comes after the
// check-in anyway.
//
// It returns a channel closed once the takeover has ended.
func startHandover(events *supervisor.KeeperEvents, tk *supervisor.Takeover, gate *windowGate, stopKeeper, exit func()) <-chan struct{} {
	tk.Done = func() {
		gate.onUI(func(ui windowUI) {
			log.Printf("fleetdeck-window: the handover is done")
			ui.handedOver()
		})
	}
	gate.onUI(func(windowUI) {
		if tk.Swapped() {
			log.Printf("fleetdeck-window: the window has checked in with LaunchServices; telling it again that the app is at %s, not %s", tk.Canonical, tk.Staged)
			go tk.Reregister()
		}
	})
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		err := events.Take(context.Background(), tk)
		if err == nil {
			return
		}
		// The old window resumes the panel it had; this one goes.
		log.Printf("fleetdeck-window: taking the panel over failed: %v", err)
		stopKeeper()
		if gate.beforeReady(func() {
			log.Printf("fleetdeck-window: quitting before the window is made; the window that started this one says why")
			exit()
		}) {
			return
		}
		gate.onUI(func(ui windowUI) { ui.terminate() })
	}()
	return ended
}
