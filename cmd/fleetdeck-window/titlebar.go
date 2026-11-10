//go:build darwin

package main

// The title bar's buttons sit at the left of the bar across the window's top
// (frame_darwin.c), on the capsules' line. The window measures where the zoom
// button ends (frame.titlebarInset); the capsule row starts past it. The panels
// are under the bar (T-226), so no page has the buttons over it.

// titlebarButtons is where the zoom button ends: x points from the window's
// left edge, 0 when the window has no such button. A frame up whose row the
// buttons now reach, or no longer reach, is laid out again.
func (c *controller) titlebarButtons(x float64) []effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	if x == c.titlebar {
		return nil
	}
	before := c.geometry()
	c.titlebar = x
	if now := c.geometry(); c.framed && now != before {
		return append([]effect{applyGeometry{G: now}}, c.boardInsets()...)
	}
	return nil
}
