//go:build darwin

package main

import "math"

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
// page as --host-inset-*. Right is what the board scrolls clear of: nothing,
// since in E the board runs on under the sessions glass, which is what the
// glass is there to show. ContentRight is what opens over the board -- a card,
// a session, a document -- keeps clear of: the sessions panel and its margin.
type insets struct{ Top, Left, Right, ContentRight float64 }

type panelWidths struct {
	Orchestrator, Sessions             float64
	OrchestratorFolded, SessionsFolded bool
}

type geometry struct {
	Orchestrator, Sessions, Capsules rect
	Board                            insets
	// Resizable: the panel has an edge to drag. A folded panel is the strip of
	// marks, with no width of its own.
	OrchestratorResizable, SessionsResizable bool
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
	// Each panel's limit is 60% of the window, so two of them can ask for more
	// than it has: the sessions panel gets at most what the orchestrator panel
	// and the margins leave, and the two never overlap.
	if room := math.Max(0, width-2*panelMargin-ow); sw > room {
		sw = room
	}
	h := height - 2*panelMargin
	o := rect{X: panelMargin, Y: panelMargin, W: ow, H: h}
	s := rect{X: width - panelMargin - sw, Y: panelMargin, W: sw, H: h}
	capX := o.X + o.W + capsuleGapLeft
	return geometry{
		Orchestrator: o,
		Sessions:     s,
		// Between the panels, or nothing when a narrow window leaves no room.
		Capsules: rect{X: capX, Y: capsuleTop, W: math.Max(0, s.X-capsuleGapRight-capX), H: capsuleHeight},
		Board:    insets{Top: boardInsetTop, Left: o.X + o.W + boardGapLeft, Right: 0, ContentRight: width - s.X},

		OrchestratorResizable: !w.OrchestratorFolded,
		SessionsResizable:     !w.SessionsFolded,
	}
}

// draggedWidth is a panel's width after its edge was dragged dx points to the
// right from where the drag began, at start. The orchestrator's edge is its
// right one, the sessions panel's its left one; either way the width stays
// within the same limits as a width set any other way.
func draggedWidth(side string, start, dx, window float64) float64 {
	w := start + dx
	if side == "sessions" {
		w = start - dx
	}
	return clampPanel(w, window, false)
}
