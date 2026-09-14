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
// page as --host-inset-*. The board keeps clear of the panels by Left and
// ContentRight: ContentRight is the sessions panel and its margin, which the
// board and what opens over it -- a card, a session, a document -- stay clear
// of. Right is always 0, and stays only while the page still reads it as
// --host-inset-right (web/js/hostactions.js, web/app.css).
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

// frameMinWidth is the window's minimum width less the capsule row's: both
// panels at their readable width, the margins and the row's gaps to them.
func frameMinWidth() float64 {
	return 2*panelMargin + 2*minPanelWidth + capsuleGapLeft + capsuleGapRight
}

// layoutFor places the two panels, the capsule row between them and the
// board's insets for a window of the given size.
func layoutFor(width, height float64, w panelWidths) geometry {
	return layoutWithRow(width, height, w, 0)
}

// layoutWithRow is layoutFor keeping rowMin points for the capsule row, its
// narrowest form (capsules_darwin.c): where the panels at their widths leave
// less, the unfolded ones narrow in proportion to what each has above its
// readable width, and no further. This is only what the window shows; the
// widths a person set are kept, and come back in a window wide enough.
func layoutWithRow(width, height float64, w panelWidths, rowMin float64) geometry {
	ow := clampPanel(w.Orchestrator, width, w.OrchestratorFolded)
	sw := clampPanel(w.Sessions, width, w.SessionsFolded)
	ow, sw = narrowForRow(width, ow, sw, w, rowMin)
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

func narrowForRow(width, ow, sw float64, w panelWidths, rowMin float64) (float64, float64) {
	excess := ow + sw - (width - 2*panelMargin - capsuleGapLeft - capsuleGapRight - rowMin)
	if rowMin <= 0 || excess <= 0 {
		return ow, sw
	}
	spare := func(pw float64, folded bool) float64 {
		if folded {
			return 0
		}
		return math.Max(0, pw-minPanelWidth)
	}
	os, ss := spare(ow, w.OrchestratorFolded), spare(sw, w.SessionsFolded)
	if os+ss == 0 {
		return ow, sw
	}
	share := math.Min(1, excess/(os+ss))
	return ow - os*share, sw - ss*share
}

// rowRoomFor is the widest a panel may be dragged to beside the other panel at
// its width, leaving the capsule row rowMin: never below the readable width.
func rowRoomFor(width, other, rowMin float64) float64 {
	return math.Max(minPanelWidth, width-2*panelMargin-capsuleGapLeft-capsuleGapRight-rowMin-other)
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
