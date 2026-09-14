//go:build darwin

package main

// The title bar's buttons float over the orchestrator panel's top corner
// (frame_darwin.c). The window measures where the zoom button ends
// (frame.titlebarInset) and the orchestrator's surface starts its header past
// it: in v0.10.0 the buttons lay on the brand.

// titlebarGap is the room between the zoom button and the header's first word.
const titlebarGap = 8.0

// titlebarInset is the zoom button's right edge in the window, x points from
// its left, 0 when the window has no such button. The orchestrator's surface
// hears of it once its page has loaded (pageLoaded) and again whenever it moves.
func (c *controller) titlebarInset(x float64) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if x == c.titlebar {
		return nil
	}
	c.titlebar = x
	return c.titlebarMessage()
}

// titlebarMessage is the orchestrator surface's inset, in its own coordinates:
// the panel starts panelMargin from the window's left edge.
func (c *controller) titlebarMessage() []effect {
	inset := 0.0
	if c.titlebar > 0 {
		inset = c.titlebar - panelMargin + titlebarGap
	}
	return c.to("orchestrator", map[string]any{"type": "titlebar", "inset": inset})
}
