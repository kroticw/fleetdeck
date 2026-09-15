//go:build darwin

package main

import (
	"encoding/json"
	"math"
	"net/url"
	"sync"
	"time"
)

// The window's decisions about the glass frame, kept apart from AppKit: each
// method takes what happened -- a page's report, a press, a resize -- and
// answers with effects, which effects.go carries out on the main thread. What
// the rules are is in the spec (sections 6.6, 6.7 and 8) and pinned by
// controller_test.go.

type effect interface{}

type (
	createSurfaces struct {
		Fleet, URL string
		Glass      glassMode
	}
	destroySurfaces struct{}
	sendTo          struct {
		Surface string
		Message map[string]any
	}
	focusSurface   struct{ Surface string }
	navigateBoard  struct{ URL string }
	openExternal   struct{ URL string }
	reloadSurface  struct{ Surface string }
	showWindowPage struct{ HTML string }
	setAppearance  struct{ Choice string }
	applyGeometry  struct{ G geometry }
	saveWidths     struct{ W panelWidths }
	setCapsules    struct{ Model json.RawMessage }
	setFrameMode   struct{ Mode glassMode }
	reloadBoard    struct{}
	setDragBand    struct{ Height float64 }
)

// hostVersion is the layout report's version this window frames; a page of
// another version stays one plain web view (web/js/host.js HOST_VERSION).
const hostVersion = 1

var sideSurfaces = []string{"orchestrator", "sessions"}

type controller struct {
	mu sync.Mutex

	baseURL    string
	widths     panelWidths
	glass      glassMode
	width      float64
	height     float64
	fullscreen bool

	// framed: the board reported a panel page and the frame is up for fleet.
	framed bool
	fleet  string
	// themeChoice is the board's theme once it has said it; "" until then.
	themeChoice string
	// loads is each side surface's page asked for, by WebKit's word and the
	// page's, as the board's is (pageWatch, spec 6.6): nothing is sent to a
	// surface whose page has not said it loaded. now is the controller's clock.
	loads map[string]*pageWatch
	now   func() time.Time
	// dragging is the panel whose edge is being dragged, "" when none.
	dragging string
	// band is how far down from the top the board's page says nothing is
	// (topBand): the band the window is dragged by, out of full screen.
	band float64
	// rowMin is the capsule row's narrowest form as last drawn (capsuleRow):
	// the room the frame keeps for it, 0 until the row is drawn.
	rowMin float64
	// titlebar is where the title bar's zoom button ends, titlebarCenter the
	// line its buttons are centred on (titlebar.go).
	titlebar, titlebarCenter float64
}

// newController frames the panel at panelURL. The window may be opened on a
// fleet's page -- a stand opens /?fleet=stand, the start page naming no fleet
// -- so the surfaces' address is built on the panel's address without its
// query.
func newController(panelURL string, widths panelWidths, glass glassMode) *controller {
	base := panelURL
	if u, err := url.Parse(panelURL); err == nil {
		u.RawQuery, u.Fragment = "", ""
		base = u.String()
	}
	return &controller{baseURL: base, widths: widths, glass: glass, loads: map[string]*pageWatch{}, now: time.Now}
}

func (c *controller) pageURL(fleet string) string {
	return c.baseURL + "?fleet=" + url.QueryEscape(fleet)
}

// to is a message for surface, or nothing: the board gets messages while the
// frame is up, a side surface only once its page has loaded.
func (c *controller) to(surface string, message map[string]any) []effect {
	if !c.framed || (surface != "board" && (c.loads[surface] == nil || !c.loads[surface].showingPanel)) {
		return nil
	}
	return []effect{sendTo{Surface: surface, Message: message}}
}

func (c *controller) insets(g geometry) []effect {
	return c.to("board", map[string]any{
		"type": "insets", "top": g.Board.Top, "left": g.Board.Left, "right": g.Board.Right, "contentRight": g.Board.ContentRight,
	})
}

func (c *controller) glassMessage(surface string) []effect {
	return c.to(surface, map[string]any{"type": "glass", "glass": string(c.glass)})
}

func (c *controller) folded(side string) bool {
	if side == "sessions" {
		return c.widths.SessionsFolded
	}
	return c.widths.OrchestratorFolded
}

// takeDown drops the frame; the caller says what replaces it.
func (c *controller) takeDown() {
	c.framed = false
	c.loads = map[string]*pageWatch{}
}

// layout is the board's report of what page it is (spec 8).
func (c *controller) layout(version int, mode, fleet string) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if version != hostVersion || mode != "panel" {
		if !c.framed {
			return nil
		}
		c.takeDown()
		return []effect{destroySurfaces{}}
	}
	g := c.geometry()
	if c.framed && fleet == c.fleet {
		// The same page loaded again: a new document, which needs its insets
		// and glass again. The surfaces are still there.
		return append(c.insets(g), c.glassMessage("board")...)
	}
	var out []effect
	if c.framed {
		out = append(out, destroySurfaces{})
	}
	c.takeDown()
	c.framed, c.fleet = true, fleet
	for _, side := range sideSurfaces {
		c.loads[side] = &pageWatch{}
		c.loads[side].ask(c.now())
	}
	out = append(out, createSurfaces{Fleet: fleet, URL: c.pageURL(fleet), Glass: c.glass}, applyGeometry{G: g})
	out = append(out, c.insets(g)...)
	return append(out, c.glassMessage("board")...)
}

// pageLoaded is a side surface's page saying where its load is (spec 6.6),
// taken as the board's page's word is: a page that loaded broken is asked for
// again after a pause, by tick; a document that has begun is given its wait.
func (c *controller) pageLoaded(surface, state string) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.loads[surface]
	if !c.framed || w == nil || !w.pageSays(state, "", c.now()) || state != pagePanel {
		return nil
	}
	var out []effect
	if c.themeChoice != "" {
		out = append(out, c.to(surface, map[string]any{"type": "theme", "choice": c.themeChoice})...)
	}
	out = append(out, c.glassMessage(surface)...)
	out = append(out, c.to(surface, map[string]any{"type": "folded", "folded": c.folded(surface)})...)
	if surface == "orchestrator" {
		out = append(out, c.to(surface, map[string]any{"type": "fullscreen", "on": c.fullscreen})...)
	}
	if surface == "orchestrator" && c.titlebar > 0 {
		out = append(out, c.titlebarMessage()...)
	}
	return out
}

// surfaceNavigated is WebKit's word about a side surface's navigation, taken
// as it is for the board's (navscreen.go).
func (c *controller) surfaceNavigated(surface string, e navEvent) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.loads[surface]
	if !c.framed || w == nil {
		return nil
	}
	v, waited := w.navSays(e, c.now())
	return c.follow(surface, v, waited)
}

// tick is time going by for the side surfaces' pages asked for.
func (c *controller) tick() []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []effect
	for _, side := range sideSurfaces {
		// A surface given up on takes the frame down, and the other's watch
		// with it.
		if w := c.loads[side]; c.framed && w != nil {
			v, waited := w.due(c.now())
			out = append(out, c.follow(side, v, waited)...)
		}
	}
	return out
}

// follow does for a side surface what its page watch says. A page given up on
// takes the frame down, and the board says why, the way it does for its own.
func (c *controller) follow(surface string, v loadVerdict, waited time.Duration) []effect {
	switch v {
	case loadAskAgain:
		c.loads[surface].ask(c.now())
		return []effect{reloadSurface{Surface: surface}}
	case loadFailed:
		page := pageFailedPage(c.pageURL(c.fleet), waited)
		c.takeDown()
		return []effect{destroySurfaces{}, showWindowPage{HTML: page}}
	case loadKeepsFalling:
		page := processLostPage(c.pageURL(c.fleet))
		c.takeDown()
		return []effect{destroySurfaces{}, showWindowPage{HTML: page}}
	}
	return nil
}

// open is opening something from any surface: cards, documents and sessions
// open in the board; the orchestrator's pinned session is its own panel.
func (c *controller) open(kind, path, short string) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.framed {
		return nil
	}
	switch kind {
	case "card", "doc":
		return append(c.to("board", map[string]any{"type": "open", "kind": kind, "path": path}), focusSurface{Surface: "board"})
	case "session":
		return append(c.to("board", map[string]any{"type": "open", "kind": kind, "short": short}), focusSurface{Surface: "board"})
	case "orchestrator":
		return append([]effect{focusSurface{Surface: "orchestrator"}}, c.to("orchestrator", map[string]any{"type": "focusTerminal"})...)
	}
	return nil
}

func (c *controller) switchFleet(name string) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []effect
	if c.framed {
		out = append(out, destroySurfaces{})
	}
	c.takeDown()
	return append(out, navigateBoard{URL: c.pageURL(name)})
}

// panel is a side surface folding or unfolding its panel.
func (c *controller) panel(side string, folded bool) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch side {
	case "orchestrator":
		c.widths.OrchestratorFolded = folded
	case "sessions":
		c.widths.SessionsFolded = folded
	default:
		return nil
	}
	out := []effect{saveWidths{W: c.widths}}
	if !c.framed {
		return out
	}
	g := c.geometry()
	out = append(out, applyGeometry{G: g})
	out = append(out, c.insets(g)...)
	return append(out, c.to(side, map[string]any{"type": "folded", "folded": folded})...)
}

// resizeStart is a press on a panel's edge: the width the drag starts from, or
// false for a panel with no edge to drag.
func (c *controller) resizeStart(side string) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.framed || (side != "orchestrator" && side != "sessions") || c.folded(side) {
		return 0, false
	}
	c.dragging = side
	if side == "sessions" {
		return c.widths.Sessions, true
	}
	return c.widths.Orchestrator, true
}

// resizeTo is the edge dragged dx points from where it was pressed. The frame
// follows the pointer; the board is told its insets once, on release: each is a
// layout of the whole board in its web view, and the glass shows the board
// under the panel meanwhile.
func (c *controller) resizeTo(side string, start, dx float64) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.framed || c.dragging != side {
		return nil
	}
	width := draggedWidth(side, start, dx, c.width)
	if side == "sessions" {
		c.widths.Sessions = c.draggedBesideRow(width, c.widths.Orchestrator, c.widths.OrchestratorFolded)
	} else {
		c.widths.Orchestrator = c.draggedBesideRow(width, c.widths.Sessions, c.widths.SessionsFolded)
	}
	return []effect{applyGeometry{G: c.geometry()}}
}

// resizeEnd is the edge let go: the width is kept, and the board learns it.
func (c *controller) resizeEnd() []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dragging == "" {
		return nil
	}
	c.dragging = ""
	out := []effect{saveWidths{W: c.widths}}
	return append(out, c.insets(c.geometry())...)
}

// theme is the board reporting the theme it cycled to.
func (c *controller) theme(choice string) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.themeChoice = choice
	out := []effect{setAppearance{Choice: choice}}
	for _, side := range sideSurfaces {
		out = append(out, c.to(side, map[string]any{"type": "theme", "choice": choice})...)
	}
	return out
}

// capsules is the board's model for the capsule row, drawn as it is.
func (c *controller) capsules(model json.RawMessage) []effect {
	return []effect{setCapsules{Model: model}}
}

// capsuleRow is the capsule row drawn, with rowMin its narrowest form: the
// frame keeps that room for it from then on, laid out again when it changes.
func (c *controller) capsuleRow(rowMin float64) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rowMin == c.rowMin {
		return nil
	}
	c.rowMin = rowMin
	if !c.framed {
		return nil
	}
	g := c.geometry()
	return append([]effect{applyGeometry{G: g}}, c.insets(g)...)
}

// geometry is the frame for the window as it is, keeping the capsule row its
// minimum.
func (c *controller) geometry() geometry {
	return layoutWithRow(c.width, c.height, c.widths, c.rowMin)
}

// draggedBesideRow is a dragged width stopped where the other panel, at its
// width, and the capsule row's minimum leave no more room.
func (c *controller) draggedBesideRow(width, other float64, otherFolded bool) float64 {
	if c.rowMin <= 0 {
		return width
	}
	return math.Min(width, rowRoomFor(c.width, clampPanel(other, c.width, otherFolded), c.rowMin))
}

// laidOut is the frame laid out as g. A geometry decided before the capsule row
// gave its minimum -- effects run in order, and a row is drawn in the middle of
// them -- is not the frame for the window as it is now; that frame is laid out
// again, whatever order the effects came in.
func (c *controller) laidOut(g geometry) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.framed {
		return nil
	}
	now := c.geometry()
	if g == now {
		return nil
	}
	return append([]effect{applyGeometry{G: now}}, c.insets(now)...)
}

func (c *controller) capsuleAction(action string) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch action {
	case "tab:board":
		return c.to("board", map[string]any{"type": "show", "section": "board"})
	case "tab:docs":
		return c.to("board", map[string]any{"type": "show", "section": "docs"})
	case "newCard":
		return c.to("board", map[string]any{"type": "newCard"})
	case "theme":
		return c.to("board", map[string]any{"type": "cycleTheme"})
	}
	return nil
}

// boardShowsOwnPage is the window putting a page of its own in the board --
// starting, taking over, a failure: no panel page, so no frame.
func (c *controller) boardShowsOwnPage() []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.framed {
		return nil
	}
	c.takeDown()
	return []effect{destroySurfaces{}}
}

func (c *controller) resized(width, height float64, fullscreen bool) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := fullscreen != c.fullscreen
	c.width, c.height, c.fullscreen = width, height, fullscreen
	var out []effect
	if c.framed {
		g := c.geometry()
		out = append([]effect{applyGeometry{G: g}}, c.insets(g)...)
	}
	if !changed {
		return out
	}
	out = append(out, c.dragBand())
	if c.framed {
		out = append(out, c.to("orchestrator", map[string]any{"type": "fullscreen", "on": fullscreen})...)
	}
	return out
}

// topBand is the board page's word on how far down from its top nothing is
// (web/js/topband.js), whatever page it shows: the band the window is dragged
// by, never taller than the board's top inset.
func (c *controller) topBand(height float64) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.band = math.Max(0, math.Min(height, boardInsetTop))
	return []effect{c.dragBand()}
}

// dragBand is the band as the window shows it: none in full screen, where a
// window is not moved.
func (c *controller) dragBand() effect {
	if c.fullscreen {
		return setDragBand{Height: 0}
	}
	return setDragBand{Height: c.band}
}

func (c *controller) glassChanged(mode glassMode) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.glass = mode
	out := append([]effect{setFrameMode{Mode: mode}}, c.glassMessage("board")...)
	for _, side := range sideSurfaces {
		out = append(out, c.glassMessage(side)...)
	}
	return out
}

// reload is every web view's page asked for again, as a person's reload: the
// board, and the surfaces, their tries counted afresh.
func (c *controller) reload() []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]effect{reloadBoard{}}, c.surfacesAskedAgain()...)
}

// reloadSurfaces is the board's page reloading: the surfaces go with it.
func (c *controller) reloadSurfaces() []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.surfacesAskedAgain()
}

func (c *controller) surfacesAskedAgain() []effect {
	var out []effect
	for _, side := range sideSurfaces {
		if w := c.loads[side]; c.framed && w != nil {
			w.tries = 0
			w.ask(c.now())
			out = append(out, reloadSurface{Surface: side})
		}
	}
	return out
}

// navigate is a side surface's web view about to go to target (spec 6.7).
// allow is the answer its navigation delegate gives at once. The policy is for
// the page itself -- its main frame, or a new window: a frame the page embeds
// navigates as the page's own content does.
func (c *controller) navigate(target string, mainFrame bool) (allow bool, effects []effect) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !mainFrame {
		return true, nil
	}
	if !c.framed {
		return false, nil
	}
	d := navigationDecision(c.baseURL, c.pageURL(c.fleet), target)
	switch {
	case d.Allow:
		return true, nil
	case d.Board != "":
		c.takeDown()
		return false, []effect{destroySurfaces{}, navigateBoard{URL: d.Board}}
	case d.External != "":
		return false, []effect{openExternal{URL: d.External}}
	}
	return false, nil
}
