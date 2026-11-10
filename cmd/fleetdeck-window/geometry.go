//go:build darwin

package main

import "math"

// The numbers are the redesign's (T-226, the "fleetdeck redesign" canvas), in
// points, with the origin at the window's top left: a bar across the whole
// window's top with the capsules on the window's buttons' line, and under it
// three islands -- the orchestrator panel, the board, the sessions panel.
const (
	panelMargin = 10.0
	// topBarHeight is the bar the capsules sit in, across the whole window.
	topBarHeight    = 2*capsuleTop + capsuleHeight
	capsuleTop      = 10.0 // centred 26 down, on the window's buttons' line
	capsuleHeight   = 32.0
	capsuleGapLeft  = 10.0
	capsuleGapRight = 12.0
	boardInsetTop   = topBarHeight
	boardGapLeft    = 18.0
	foldedWidth     = 48.0
	minPanelWidth   = 220.0 // web/js/columnwidth.js MIN_PIXELS
	maxPanelShare   = 0.6   // web/js/columnwidth.js MAX_PERCENT
	// minCenterWidth is the narrowest the board's island between the panels
	// gets: the panels narrow for it, and the window is no narrower.
	minCenterWidth = 360.0
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

// frameMinWidth is the window's narrowest: both panels at their readable
// width, the board's island at its narrowest, and the margins between. The
// capsule row asks for its own (capsules_darwin.c), and the wider wins.
func frameMinWidth() float64 {
	return 4*panelMargin + 2*minPanelWidth + minCenterWidth
}

// layoutFor places the two panels under the top bar, the capsule row across
// the bar and the board's insets for a window of the given size.
func layoutFor(width, height float64, w panelWidths) geometry {
	return layoutPastButtons(width, height, w, 0)
}

// layoutPastButtons is layoutFor with the window's buttons ending buttonsEnd
// points from its left edge on the bar's line, 0 with none there (full
// screen): the capsule row begins its gap past them, or at the margin. The
// panels are under the bar and never meet the row; where the window is too
// narrow for both at their widths and the board's island at its narrowest,
// the unfolded ones narrow in proportion to what each has above its readable
// width, and no further. This is only what the window shows; the widths a
// person set are kept, and come back in a window wide enough.
func layoutPastButtons(width, height float64, w panelWidths, buttonsEnd float64) geometry {
	ow := clampPanel(w.Orchestrator, width, w.OrchestratorFolded)
	sw := clampPanel(w.Sessions, width, w.SessionsFolded)
	ow, sw = narrowForCenter(width, ow, sw, w)
	// Each panel's limit is 60% of the window, so two of them can ask for more
	// than it has: the sessions panel gets at most what the orchestrator panel
	// and the margins leave, and the two never overlap.
	if room := math.Max(0, width-2*panelMargin-ow); sw > room {
		sw = room
	}
	h := math.Max(0, height-topBarHeight-panelMargin)
	o := rect{X: panelMargin, Y: topBarHeight, W: ow, H: h}
	s := rect{X: width - panelMargin - sw, Y: topBarHeight, W: sw, H: h}
	capX := panelMargin
	if buttonsEnd > 0 {
		capX = buttonsEnd + capsuleGapLeft
	}
	return geometry{
		Orchestrator: o,
		Sessions:     s,
		// Across the bar, from past the window's buttons to the right margin.
		Capsules: rect{X: capX, Y: capsuleTop, W: math.Max(0, width-panelMargin-capX), H: capsuleHeight},
		Board:    insets{Top: boardInsetTop, Left: o.X + o.W + boardGapLeft, Right: 0, ContentRight: width - s.X},

		OrchestratorResizable: !w.OrchestratorFolded,
		SessionsResizable:     !w.SessionsFolded,
	}
}

func narrowForCenter(width, ow, sw float64, w panelWidths) (float64, float64) {
	excess := ow + sw - centerRoom(width)
	if excess <= 0 {
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

// centerRoom is what both panels together may take in a window this wide,
// leaving the board's island its narrowest.
func centerRoom(width float64) float64 {
	return width - 4*panelMargin - minCenterWidth
}

// roomBeside is the widest a panel may be dragged to beside the other panel at
// its width, leaving the board's island its narrowest: never below the
// readable width.
func roomBeside(width, other float64) float64 {
	return math.Max(minPanelWidth, centerRoom(width)-other)
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
