//go:build darwin

package main

import (
	"encoding/json"
	"log"
	"time"

	webview "github.com/webview/webview_go"
)

// glassWindow is the glass frame at work in the running window: the controller
// deciding, and the frame, the surfaces and the capsules carrying its effects
// out (natives). Its state is the main thread's; reports from anywhere else
// reach it through the web view's Dispatch.
type glassWindow struct {
	w        webview.WebView
	panelURL string
	frame    *frame
	bridge   *bridge
	ctl      *controller
	mode     glassMode

	surfaces map[string]*surface
	// A load not heard from in time is a broken one (spec 6.6). generation
	// tells a timer of a surface since replaced from one of the surface now.
	timers     map[string]*time.Timer
	generation map[string]int
	framed     bool
	model      json.RawMessage

	// reloadBoard asks for the board's page again, the way the page's own
	// reload does (main.go).
	reloadBoard func()
}

func newGlassWindow(w webview.WebView, panelURL string, reloadBoard func()) *glassWindow {
	mode := currentGlassMode()
	g := &glassWindow{
		w:           w,
		panelURL:    panelURL,
		frame:       installFrame(w.Window()),
		bridge:      newBridge(),
		mode:        mode,
		surfaces:    map[string]*surface{},
		timers:      map[string]*time.Timer{},
		generation:  map[string]int{},
		reloadBoard: reloadBoard,
	}
	g.ctl = newController(panelURL, loadPanelWidths(), mode)
	g.frame.setMode(mode)
	width, height := windowContentSize(w.Window())
	g.run(g.ctl.resized(width, height, windowIsFullscreen(w.Window())))

	// A surface's page says where its load is through the same binding the
	// board's does; the board's own goes to the screen (main.go).
	g.bridge.handle(pageLoadedBindingName, func(surface string, args json.RawMessage) (any, error) {
		var state string
		if err := json.Unmarshal(args, &state); err != nil {
			return nil, err
		}
		g.w.Dispatch(func() { g.pageLoaded(surface, state) })
		return nil, nil
	})
	g.bind("fleetdeckLayout", func(_ string, args json.RawMessage) (any, error) {
		var report struct {
			Version int    `json:"version"`
			Mode    string `json:"mode"`
			Fleet   string `json:"fleet"`
		}
		if err := json.Unmarshal(args, &report); err != nil {
			return nil, err
		}
		g.later(g.ctl.layout(report.Version, report.Mode, report.Fleet))
		return nil, nil
	})
	g.bind("fleetdeckOpen", func(_ string, args json.RawMessage) (any, error) {
		var open struct {
			Kind  string `json:"kind"`
			Path  string `json:"path"`
			Short string `json:"short"`
		}
		if err := json.Unmarshal(args, &open); err != nil {
			return nil, err
		}
		g.later(g.ctl.open(open.Kind, open.Path, open.Short))
		return nil, nil
	})
	g.bind("fleetdeckSwitchFleet", func(_ string, args json.RawMessage) (any, error) {
		var name string
		if err := json.Unmarshal(args, &name); err != nil {
			return nil, err
		}
		g.later(g.ctl.switchFleet(name))
		return nil, nil
	})
	g.bind("fleetdeckCapsules", func(_ string, args json.RawMessage) (any, error) {
		g.later(g.ctl.capsules(args))
		return nil, nil
	})
	g.bind("fleetdeckTheme", func(_ string, args json.RawMessage) (any, error) {
		var choice string
		if err := json.Unmarshal(args, &choice); err != nil {
			return nil, err
		}
		g.later(g.ctl.theme(choice))
		return nil, nil
	})
	g.bind("fleetdeckPanel", func(_ string, args json.RawMessage) (any, error) {
		var fold struct {
			Side   string `json:"side"`
			Folded bool   `json:"folded"`
		}
		if err := json.Unmarshal(args, &fold); err != nil {
			return nil, err
		}
		g.later(g.ctl.panel(fold.Side, fold.Folded))
		return nil, nil
	})

	setSurfaceEvents(g.surfaceMessage, g.surfaceNavigation)
	setCapsuleEvents(func(action string) { g.run(g.ctl.capsuleAction(action)) })
	setWindowEvents(g.windowChanged)
	setMenuReload(func() { g.run(g.ctl.reload()) })
	observeWindow(w.Window())
	return g
}

// bind makes a binding the board reaches through webview_go and the surfaces
// through the registry.
func (g *glassWindow) bind(name string, h bridgeHandler) {
	g.bridge.handle(name, h)
	if err := g.w.Bind(name, func(args json.RawMessage) (any, error) { return g.bridge.call("board", name, args) }); err != nil {
		log.Printf("fleetdeck-window: the board page will not reach %s: %v", name, err)
	}
}

// share puts a binding the board already has into the registry, for the
// surfaces: the update button lives in the orchestrator's surface.
func (g *glassWindow) share(name string, f func() (any, error)) {
	g.bridge.handle(name, func(string, json.RawMessage) (any, error) { return f() })
}

// later carries effects out on the main thread, after whatever callback
// produced them has returned: a surface is never taken down inside its own.
func (g *glassWindow) later(effects []effect) {
	if len(effects) == 0 {
		return
	}
	g.w.Dispatch(func() { g.run(effects) })
}

func (g *glassWindow) run(effects []effect) { runEffects(g, effects) }

// boardShowsOwnPage is main.go's show putting a page of the window's own into
// the board.
func (g *glassWindow) boardShowsOwnPage() { g.run(g.ctl.boardShowsOwnPage()) }

// reloadSurfaces is the board's page reloading: the surfaces go with it.
func (g *glassWindow) reloadSurfaces() {
	for kind, s := range g.surfaces {
		s.reload()
		g.arm(kind, pageLoadWait)
	}
}

func (g *glassWindow) surfaceMessage(surface, message string) {
	go func() {
		reply := answerSurfaceCall(g.bridge, surface, message)
		if reply == "" {
			return
		}
		g.w.Dispatch(func() {
			if s := g.surfaces[surface]; s != nil {
				s.eval(reply)
			}
		})
	}()
}

func (g *glassWindow) surfaceNavigation(_, target string) bool {
	allow, effects := g.ctl.navigate(target)
	g.later(effects)
	return allow
}

func (g *glassWindow) pageLoaded(surface, state string) {
	switch state {
	case pageLoading:
		g.arm(surface, pageLoadingWait)
	default:
		g.disarm(surface)
	}
	g.run(g.ctl.pageLoaded(surface, state))
}

func (g *glassWindow) windowChanged(kind string) {
	switch kind {
	case "glass":
		g.run(g.ctl.glassChanged(currentGlassMode()))
	default:
		width, height := windowContentSize(g.w.Window())
		g.run(g.ctl.resized(width, height, windowIsFullscreen(g.w.Window())))
	}
}

func (g *glassWindow) arm(surface string, wait time.Duration) {
	g.disarm(surface)
	g.generation[surface]++
	generation := g.generation[surface]
	g.timers[surface] = time.AfterFunc(wait, func() {
		g.w.Dispatch(func() {
			if g.generation[surface] != generation || g.surfaces[surface] == nil {
				return
			}
			log.Printf("fleetdeck-window: the %s surface's page did not say it loaded within %s", surface, wait)
			g.run(g.ctl.pageLoaded(surface, pageBroken))
		})
	})
}

func (g *glassWindow) disarm(surface string) {
	if t := g.timers[surface]; t != nil {
		t.Stop()
		delete(g.timers, surface)
	}
}

// --- natives ---------------------------------------------------------------------

func (g *glassWindow) createSurface(kind, url string, glass glassMode) {
	if old := g.surfaces[kind]; old != nil {
		old.close()
	}
	s := newSurface(g.frame.board(), g.frame.panelContent(kind), kind, g.panelURL, glass, g.bridge)
	g.surfaces[kind] = s
	g.framed = true
	s.load(url)
	g.arm(kind, pageLoadWait)
	g.redrawCapsules()
}

func (g *glassWindow) destroySurfaces() {
	for kind, s := range g.surfaces {
		g.disarm(kind)
		g.generation[kind]++
		s.close()
	}
	g.surfaces = map[string]*surface{}
	g.framed = false
	// No panel page, no frame (spec 5.5): the panels and the capsules fold
	// away until the board reports a panel again.
	g.frame.layout(geometry{})
	clearCapsules(g.frame.capsules())
}

func (g *glassWindow) send(surface string, msg map[string]any) {
	if surface == "board" {
		g.w.Eval(receiveScript(msg))
		return
	}
	if s := g.surfaces[surface]; s != nil {
		s.send(msg)
	}
}

func (g *glassWindow) focus(surface string) {
	if surface == "board" {
		focusView(g.frame.board())
		return
	}
	if s := g.surfaces[surface]; s != nil {
		s.focus()
	}
}

func (g *glassWindow) navigateBoard(url string) { g.w.Navigate(url) }
func (g *glassWindow) openExternal(url string)  { openExternalURL(url) }

func (g *glassWindow) reloadSurface(surface string) {
	if s := g.surfaces[surface]; s != nil {
		s.reload()
		g.arm(surface, pageLoadWait)
	}
}

func (g *glassWindow) showFailedPage() {
	g.w.SetHtml(pageFailedPage(g.panelURL, pageLoadingWait))
}

func (g *glassWindow) setAppearance(choice string) { applyAppearance(choice) }
func (g *glassWindow) applyGeometry(geo geometry)  { g.frame.layout(geo) }
func (g *glassWindow) saveWidths(w panelWidths)    { storePanelWidths(w) }

func (g *glassWindow) setCapsules(model json.RawMessage) {
	g.model = model
	g.redrawCapsules()
}

func (g *glassWindow) setFrameMode(m glassMode) {
	g.mode = m
	g.frame.setMode(m)
	g.redrawCapsules()
}

func (g *glassWindow) reloadAll() {
	g.reloadBoard()
	g.reloadSurfaces()
}

func (g *glassWindow) redrawCapsules() {
	if !g.framed || g.model == nil {
		return
	}
	m, err := parseCapsuleModel(g.model)
	if err != nil {
		log.Printf("fleetdeck-window: the capsules are not drawn: %v", err)
		return
	}
	drawCapsules(g.frame.capsules(), m, g.mode)
}

// broadcast is the board's web view with Eval reaching the surfaces too: the
// update's reports go to every page, since the update button lives in the
// orchestrator's surface.
type broadcast struct {
	webview.WebView
	g *glassWindow
}

func (b broadcast) Eval(js string) {
	b.WebView.Eval(js)
	for _, s := range b.g.surfaces {
		s.eval(js)
	}
}

func (g *glassWindow) view() webview.WebView { return broadcast{WebView: g.w, g: g} }
