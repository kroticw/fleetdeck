//go:build darwin

package main

import (
	"encoding/json"
	"net/url"
	"sync"
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
	showFailedPage struct{}
	setAppearance  struct{ Choice string }
	applyGeometry  struct{ G geometry }
	saveWidths     struct{ W panelWidths }
	setCapsules    struct{ Model json.RawMessage }
	setFrameMode   struct{ Mode glassMode }
	reloadAll      struct{}
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
	// ready: the surface's page said it loaded (spec 6.6); nothing is sent to
	// a surface that has not. tries counts its failed loads in a row.
	ready map[string]bool
	tries map[string]int
}

func newController(baseURL string, widths panelWidths, glass glassMode) *controller {
	return &controller{baseURL: baseURL, widths: widths, glass: glass, ready: map[string]bool{}, tries: map[string]int{}}
}

func (c *controller) pageURL(fleet string) string {
	return c.baseURL + "?fleet=" + url.QueryEscape(fleet)
}

// to is a message for surface, or nothing: the board gets messages while the
// frame is up, a side surface only once its page has loaded.
func (c *controller) to(surface string, message map[string]any) []effect {
	if !c.framed || (surface != "board" && !c.ready[surface]) {
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
	c.ready = map[string]bool{}
	c.tries = map[string]int{}
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
	g := layoutFor(c.width, c.height, c.widths)
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
	out = append(out, createSurfaces{Fleet: fleet, URL: c.pageURL(fleet), Glass: c.glass}, applyGeometry{G: g})
	out = append(out, c.insets(g)...)
	return append(out, c.glassMessage("board")...)
}

// pageLoaded is a side surface's page saying where its load is (spec 6.6).
func (c *controller) pageLoaded(surface, state string) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.framed || (surface != "orchestrator" && surface != "sessions") {
		return nil
	}
	switch state {
	case pagePanel:
		c.ready[surface], c.tries[surface] = true, 0
		var out []effect
		if c.themeChoice != "" {
			out = append(out, c.to(surface, map[string]any{"type": "theme", "choice": c.themeChoice})...)
		}
		out = append(out, c.glassMessage(surface)...)
		out = append(out, c.to(surface, map[string]any{"type": "folded", "folded": c.folded(surface)})...)
		if surface == "orchestrator" {
			out = append(out, c.to(surface, map[string]any{"type": "fullscreen", "on": c.fullscreen})...)
		}
		return out
	case pageLeaving:
		c.ready[surface] = false
		return nil
	case pageBroken:
		c.ready[surface] = false
		c.tries[surface]++
		if c.tries[surface] < pageLoadTries {
			return []effect{reloadSurface{Surface: surface}}
		}
		c.takeDown()
		return []effect{destroySurfaces{}, showFailedPage{}}
	}
	// pageLoading: the document has begun; readiness comes with panel.
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
	g := layoutFor(c.width, c.height, c.widths)
	out = append(out, applyGeometry{G: g})
	out = append(out, c.insets(g)...)
	return append(out, c.to(side, map[string]any{"type": "folded", "folded": folded})...)
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
	if !c.framed {
		return nil
	}
	g := layoutFor(width, height, c.widths)
	out := append([]effect{applyGeometry{G: g}}, c.insets(g)...)
	if changed {
		out = append(out, c.to("orchestrator", map[string]any{"type": "fullscreen", "on": fullscreen})...)
	}
	return out
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

func (c *controller) reload() []effect { return []effect{reloadAll{}} }

// navigate is a side surface's web view about to go to target (spec 6.7).
// allow is the answer its navigation delegate gives at once.
func (c *controller) navigate(target string) (allow bool, effects []effect) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
