//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
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
	framed   bool
	model    json.RawMessage

	// askBoard asks for the board's page again, the way the page's own reload
	// does; putUp puts a page of the window's own in the board, the way the
	// screen's are put up (main.go).
	askBoard func()
	putUp    func(page string)

	// The drag on a panel's edge under way: which panel, the width it began
	// at, and the pointer's x at the press.
	dragSide  string
	dragWidth float64
	dragX     float64

	// On a stand: its trip into full screen, and the frame as the window last
	// said it measured it.
	standFS   *standFullScreen
	frameSaid string
}

func newGlassWindow(w webview.WebView, panelURL string, askBoard func(), putUp func(page string)) *glassWindow {
	mode := currentGlassMode()
	g := &glassWindow{
		w:        w,
		panelURL: panelURL,
		frame:    installFrame(w.Window()),
		bridge:   newBridge(),
		mode:     mode,
		surfaces: map[string]*surface{},
		askBoard: askBoard,
		putUp:    putUp,
		standFS:  newStandFullScreen(hostOnStand && standFullScreenOn),
	}
	g.ctl = newController(panelURL, panelWidthsForStand(loadPanelWidths(), standFold), mode)
	g.frame.setMode(mode)
	logFrameMode(mode)
	width, height := windowContentSize(w.Window())
	g.run(g.ctl.resized(width, height, windowIsFullscreen(w.Window())))
	g.run(g.ctl.titlebarButtons(g.frame.titlebarInset(), g.frame.titlebarCenter()))

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
	g.bindBoard("fleetdeckLayout", func(_ string, args json.RawMessage) (any, error) {
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
	// The empty band at the top of whatever page the board shows, which the
	// window is dragged by (web/js/topband.js).
	g.bindBoard(topBandBindingName, func(_ string, args json.RawMessage) (any, error) {
		var height float64
		if err := json.Unmarshal(args, &height); err != nil {
			return nil, err
		}
		g.later(g.ctl.topBand(height))
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
	g.bindBoard("fleetdeckCapsules", func(_ string, args json.RawMessage) (any, error) {
		g.later(g.ctl.capsules(args))
		return nil, nil
	})
	g.bindBoard("fleetdeckTheme", func(_ string, args json.RawMessage) (any, error) {
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

	handleStandReports(g.bridge, hostOnStand, log.Printf)
	setSurfaceEvents(g.surfaceMessage, g.surfaceNavigation)
	setCapsuleEvents(func(action string) { g.run(g.ctl.capsuleAction(action)) })
	setWindowEvents(g.windowChanged)
	setMenuReload(func() { g.run(g.ctl.reload()) })
	setResizeEvents(g.resize)
	observeSurfaceNavigation(g.surfaceNavigated)
	observeWindow(w.Window())
	return g
}

// bind makes a binding the board reaches through webview_go and the surfaces
// through the registry.
func (g *glassWindow) bind(name string, h bridgeHandler) {
	g.bridge.handle(name, h)
	g.bindForBoard(name)
}

// bindBoard makes a binding only the board reaches: what it reports of itself.
func (g *glassWindow) bindBoard(name string, h bridgeHandler) {
	g.bridge.handleBoard(name, h)
	g.bindForBoard(name)
}

func (g *glassWindow) bindForBoard(name string) {
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
func (g *glassWindow) reloadSurfaces() { g.run(g.ctl.reloadSurfaces()) }

// tick is time going by for the surfaces' pages asked for (main.go's ticker).
func (g *glassWindow) tick() { g.run(g.ctl.tick()) }

// surfaceMessage is a surface's page calling a binding, on the main thread: it
// joins that web view's queue (callQueue), answered in order off the main
// thread.
func (g *glassWindow) surfaceMessage(surface, message, origin string, mainFrame bool) {
	if !acceptSurfaceMessage(g.panelURL, origin, mainFrame) {
		log.Printf("fleetdeck-window: a binding call in the %s surface from %q (main frame: %v) is not the panel's page, and is refused", surfaceKind(surface), origin, mainFrame)
		return
	}
	// Only into the queue of the surface of its generation: a call from a
	// surface taken down is not the one shown now's (surfacename.go).
	if s := surfaceNamed(g.surfaces, surface); s != nil && s.calls != nil {
		s.calls.push(message)
	}
}

// answerCalls is what a surface's queue does with each call: answers it, and
// settles its promise in that same web view, if it is still the one shown.
func (g *glassWindow) answerCalls(kind string, s *surface) func(message string) {
	return func(message string) {
		// Under the surface's name, so a binding tells its generation
		// (pageLoaded).
		reply := answerSurfaceCall(g.bridge, surfaceName(kind, s.gen), message)
		if reply == "" {
			return
		}
		g.w.Dispatch(func() {
			if replyGoesTo(g.surfaces, kind, s) {
				s.eval(reply)
			}
		})
	}
}

func (g *glassWindow) surfaceNavigation(surface, target string, mainFrame bool) bool {
	allow, effects := g.ctl.navigateFrom(surface, target, mainFrame)
	g.later(effects)
	return allow
}

// pageLoaded is a side surface's page saying where its load is. A word from a
// surface not shown now is not about the page shown now: it is not logged as
// that page's, which scripts/ci-window-stand.sh waits for, and a stand does not
// count it toward entering full screen.
func (g *glassWindow) pageLoaded(name, state string) {
	surface, shown := g.ctl.current(name)
	if !shown {
		return
	}
	log.Printf("fleetdeck-window: %s", surfacePageSays(surface, state))
	g.run(g.ctl.pageLoadedFrom(name, state))
	if state != pagePanel {
		return
	}
	if after, ok := g.standFS.surfaceLoaded(surface); ok {
		log.Printf("fleetdeck-window: on this stand the window enters full screen in %v", after)
		g.toggleFullScreenAfter(after)
	}
}

// revealOnStand measures the frame in full screen a few times before the window
// leaves, while the stand's script brings the pointer to the top of the screen
// and away (scripts/standpointer): what it brings out over the content need not
// tell the window anything. Each step is logged and the frame measured after
// it.
func (g *glassWindow) revealOnStand(trip int) {
	at := func(after time.Duration, what string, do func()) {
		time.AfterFunc(after, func() {
			g.w.Dispatch(func() {
				log.Printf("fleetdeck-window: on this stand, full screen %d: %s", trip, what)
				do()
				g.reportFrame()
			})
		})
	}
	for _, after := range standRevealMeasures {
		at(after, "the frame is measured", func() {})
	}
}

func (g *glassWindow) toggleFullScreenAfter(after time.Duration) {
	time.AfterFunc(after, func() { g.w.Dispatch(func() { toggleFullScreen(g.w.Window()) }) })
}

// reportFrame puts what the frame measures natively in the window's log, on a
// stand only: once AppKit has laid the window out, and only when it changed
// (standframe_darwin.go).
func (g *glassWindow) reportFrame() {
	if !hostOnStand {
		return
	}
	g.w.Dispatch(func() {
		if !g.framed {
			return
		}
		line := frameReportLine(measureFrame(g.frame, g.w.Window(), g.mode))
		if line == g.frameSaid {
			return
		}
		g.frameSaid = line
		log.Print(line)
	})
}

// surfacePageSays is the window's log line for a surface's page's word about
// itself. scripts/ci-window-stand.sh waits for it; glasswindow_test.go holds
// both sides.
func surfacePageSays(surface, state string) string {
	return fmt.Sprintf("the %s surface's page says %q", surface, state)
}

// surfaceNavigated is WebKit's word about a surface's navigation, from inside
// its delegate: carried out later, since it may take that web view down.
func (g *glassWindow) surfaceNavigated(name string, e navEvent) {
	log.Printf("fleetdeck-window: the %s surface's navigation %s", surfaceKind(name), e)
	g.later(g.ctl.surfaceNavigatedFrom(name, e))
}

func (g *glassWindow) windowChanged(kind string) {
	switch kind {
	case "glass":
		g.run(g.ctl.glassChanged(currentGlassMode()))
	default:
		width, height := windowContentSize(g.w.Window())
		fullscreen := windowIsFullscreen(g.w.Window())
		g.run(g.ctl.resized(width, height, fullscreen))
		g.run(g.ctl.titlebarButtons(g.frame.titlebarInset(), g.frame.titlebarCenter()))
		if kind == "fullscreen" {
			if after, ok := g.standFS.changed(fullscreen); ok {
				verb := "leaves"
				if !fullscreen {
					verb = "enters again"
				}
				log.Printf("fleetdeck-window: on this stand the window %s full screen in %v", verb, after)
				g.toggleFullScreenAfter(after)
				if fullscreen {
					g.revealOnStand(g.standFS.trip())
				}
			}
		}
		g.reportFrame()
	}
}

// resize is the width strip at a panel's edge (frame_darwin.c), on the main
// thread: the frame follows every drag in this process, and the board hears of
// the new width on release.
func (g *glassWindow) resize(side string, phase int, x float64) {
	switch phase {
	case 0:
		width, ok := g.ctl.resizeStart(side)
		if !ok {
			g.dragSide = ""
			return
		}
		g.dragSide, g.dragWidth, g.dragX = side, width, x
	case 1:
		if g.dragSide == side {
			g.run(g.ctl.resizeTo(side, g.dragWidth, x-g.dragX))
		}
	case 2:
		g.dragSide = ""
		g.run(g.ctl.resizeEnd())
	}
}

// --- natives ---------------------------------------------------------------------

func (g *glassWindow) createSurface(kind, url string, glass glassMode, gen int) {
	if old := g.surfaces[kind]; old != nil {
		old.close()
	}
	s := newSurface(g.frame.board(), g.frame.panelContent(kind), kind, gen, g.panelURL, glass, g.bridge)
	s.calls = newCallQueue(g.answerCalls(kind, s))
	g.surfaces[kind] = s
	g.framed = true
	s.load(url)
	g.redrawCapsules()
}

func (g *glassWindow) destroySurfaces() {
	for _, s := range g.surfaces {
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
	}
}

func (g *glassWindow) showWindowPage(page string) { g.putUp(page) }

// setAppearance is the app's theme changing: the capsules are drawn again in it,
// whether or not the board's page has sent their model again first.
func (g *glassWindow) setAppearance(choice string) {
	applyAppearance(choice)
	g.redrawCapsules()
}
func (g *glassWindow) applyGeometry(geo geometry) {
	g.frame.layout(geo)
	g.run(g.ctl.laidOut(geo))
	g.reportFrame()
}
func (g *glassWindow) saveWidths(w panelWidths) {
	if storesWidths(standFold) {
		storePanelWidths(w)
	}
}

func (g *glassWindow) setCapsules(model json.RawMessage) {
	g.model = model
	g.redrawCapsules()
}

func (g *glassWindow) setFrameMode(m glassMode) {
	g.mode = m
	g.frame.setMode(m)
	logFrameMode(m)
	g.redrawCapsules()
}

// logFrameMode is the window's log line for what the frame is drawn in: glass,
// or vibrancy or opaque when the system has no glass or asks for less
// transparency or more contrast. A stand's screenshot cannot tell them apart.
func logFrameMode(m glassMode) {
	log.Printf("fleetdeck-window: the frame is drawn in %s", m)
}

func (g *glassWindow) reloadBoard() { g.askBoard() }

func (g *glassWindow) setDragBand(height float64) {
	g.frame.setDragBand(height)
	// The band is what the window is dragged by, and the page's word about it
	// comes long after the frame is first laid out: a stand measures the frame
	// again with it (standframe_darwin.go).
	g.reportFrame()
}

func (g *glassWindow) showToolbar(visible bool) { setToolbarVisible(g.w.Window(), visible) }

func (g *glassWindow) boardInsets() { g.run(g.ctl.boardInsetsNow()) }

func (g *glassWindow) redrawCapsules() {
	if !g.framed || g.model == nil {
		return
	}
	drawCapsuleRow(g.frame, g.model, g.mode, g.ctl, g.run)
	g.reportFrame()
}

// drawCapsuleRow draws the board's capsule model into the frame's row and gives
// the controller the row's minimum, whose effects run carries out. The window's
// stand probe (capsulestand_darwin.go) draws the row the same way.
func drawCapsuleRow(f *frame, model json.RawMessage, mode glassMode, ctl *controller, run func([]effect)) {
	m, err := parseCapsuleModel(model)
	if err != nil {
		log.Printf("fleetdeck-window: the capsules are not drawn: %v", err)
		return
	}
	run(ctl.capsuleRow(drawCapsules(f.capsules(), m, mode)))
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
