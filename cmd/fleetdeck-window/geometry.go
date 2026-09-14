//go:build darwin

package main

// The numbers are the chosen design's (canvas page "Выбрано", spec 5.2), in
// points, with the origin at the window's top left.
const (
	panelMargin     = 8.0
	capsuleTop      = 12.0
	capsuleHeight   = 32.0
	capsuleGapLeft  = 10.0
	capsuleGapRight = 12.0
	boardInsetTop   = 64.0
	boardGapLeft    = 18.0
	foldedWidth     = 48.0
	minPanelWidth   = 220.0 // web/js/columnwidth.js MIN_PIXELS
	maxPanelShare   = 0.6   // web/js/columnwidth.js MAX_PERCENT
)

type rect struct{ X, Y, W, H float64 }

// insets is the room the frame takes from the board's web view, sent to the
// page as --host-inset-*.
type insets struct{ Top, Left, Right float64 }

type panelWidths struct {
	Orchestrator, Sessions             float64
	OrchestratorFolded, SessionsFolded bool
}

type geometry struct {
	Orchestrator, Sessions, Capsules rect
	Board                            insets
}

// glassMode is what sits under the side surfaces: Liquid Glass, the vibrancy
// material of a system without it, or nothing when transparency is reduced.
type glassMode string

const (
	glassModeGlass    glassMode = "glass"
	glassModeVibrancy glassMode = "vibrancy"
	glassModeOpaque   glassMode = "opaque"
)

func clampPanel(w, window float64, folded bool) float64 {
	if folded {
		return foldedWidth
	}
	if w < minPanelWidth {
		w = minPanelWidth
	}
	if limit := window * maxPanelShare; w > limit {
		w = limit
	}
	return w
}

// layoutFor places the two panels, the capsule row between them and the
// board's insets for a window of the given size.
func layoutFor(width, height float64, w panelWidths) geometry {
	ow := clampPanel(w.Orchestrator, width, w.OrchestratorFolded)
	sw := clampPanel(w.Sessions, width, w.SessionsFolded)
	h := height - 2*panelMargin
	o := rect{X: panelMargin, Y: panelMargin, W: ow, H: h}
	s := rect{X: width - panelMargin - sw, Y: panelMargin, W: sw, H: h}
	capX := o.X + o.W + capsuleGapLeft
	return geometry{
		Orchestrator: o,
		Sessions:     s,
		Capsules:     rect{X: capX, Y: capsuleTop, W: s.X - capsuleGapRight - capX, H: capsuleHeight},
		Board:        insets{Top: boardInsetTop, Left: o.X + o.W + boardGapLeft, Right: width - s.X},
	}
}
