//go:build darwin

package main

// The title bar's buttons sit in the orchestrator panel's top corner
// (frame_darwin.c). The window measures where the zoom button ends and the line
// the buttons are centred on (frame.titlebarInset, frame.titlebarCenter); the
// orchestrator's surface starts its header past them, its row on that line. In
// v0.10.0 the buttons lay on the brand, and in v0.10.1 above it.

// titlebarGap is the room between the zoom button and the header's first word.
const titlebarGap = 8.0

// titlebarButtons is where the title bar's buttons are: x, the zoom button's
// right edge, and center, the line they are centred on, in points from the
// window's left and top edges; 0 for each when the window has no such button.
// The orchestrator's surface hears of them once its page has loaded
// (pageLoaded) and again whenever they move. The capsule row keeps past them
// (layoutPastButtons): a frame up whose row they now reach, or no longer reach,
// is laid out again.
func (c *controller) titlebarButtons(x, center float64) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if x == c.titlebar && center == c.titlebarCenter {
		return nil
	}
	before := c.geometry()
	c.titlebar, c.titlebarCenter = x, center
	var out []effect
	if now := c.geometry(); c.framed && now != before {
		out = append(append(out, applyGeometry{G: now}), c.boardInsets()...)
	}
	return append(out, c.titlebarMessage()...)
}

// titlebarMessage is the orchestrator surface's inset and line, in its own
// coordinates: the panel starts panelMargin from the window's left and top
// edges.
func (c *controller) titlebarMessage() []effect {
	inset, line := 0.0, 0.0
	if c.titlebar > 0 {
		inset = c.titlebar - panelMargin + titlebarGap
	}
	if c.titlebarCenter > 0 {
		line = c.titlebarCenter - panelMargin
	}
	return c.to("orchestrator", map[string]any{"type": "titlebar", "inset": inset, "center": line})
}
