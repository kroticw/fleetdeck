// Command standcheck reads the window's log from a CI stand
// (scripts/ci-window-stand.sh) and says whether the frame keeps what the
// operator saw broken in v0.10.1, by what the window measured of itself
// natively (cmd/fleetdeck-window/standframe_darwin.go) and what the pages
// measured of themselves (web/js/standheader.js, web/js/standreport.js):
//
//   - every capsule is inside the row and over neither panel, out of full
//     screen, in it, and after each time it left;
//   - the board meets both panels as they are laid out, and its last column
//     comes out from under the sessions panel, in each of those;
//   - the selected tab is a capsule, where the system has border shapes;
//   - the window's buttons sit concentric in the orchestrator panel's corner,
//     before full screen and after each time it left, and the header's row and
//     its brand are centred on their line;
//   - in full screen nothing of the title bar keeps the window's top or lies
//     shown over the capsule row.
//
// The board's own verdict on its last column -- worked out from the insets the
// page was sent -- is not read: in v0.10.1 it said true while the insets were
// stale. The board is held to the native panels' frames.
//
// Usage: standcheck -log <window.log> [-fullscreen-trips <n>]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
)

const (
	framePrefix  = "fleetdeck-window: the frame measures "
	headerPrefix = "fleetdeck-window: the orchestrator surface reports its scrolling: "
	boardPrefix  = "fleetdeck-window: the board reports its scrolling: "
)

const (
	// The orchestrator panel's corner radius (cmd/fleetdeck-window/frame_darwin.c).
	panelRadius = 18
	// The board's inset past the orchestrator panel
	// (cmd/fleetdeck-window/geometry.go boardGapLeft); the board's own box
	// starts inside it.
	boardGapLeft = 18
)

// Slack: sub-point layout for an edge, a point for a line, two for a line of
// text, whose box is the font's.
const (
	edgeSlack  = 0.5
	lineSlack  = 1
	brandSlack = 2
	// A capsule's top row starts further in than a rounded rectangle's drawn
	// in its place, by this much at the least: 0.46 against 0.19 on a 2x
	// screen. On the 1x runner a capsule measured 0.375, and 0.29 once the
	// window had been in full screen, so no fixed number holds a capsule.
	capsuleOverRoundedInset = 0.05
	// An overlay this transparent shows nothing.
	clearAlpha = 0.01
)

type box struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

type capsule struct {
	Name string `json:"name"`
	box
}

// overlay is what may lie over the window's content at its top: the title bar's
// container, or another window of the app over this one.
type overlay struct {
	Kind string `json:"kind"`
	box
	Visible bool    `json:"visible"`
	Alpha   float64 `json:"alpha"`
}

// frameReport is the window's word on its frame, in points from the window's
// top left.
type frameReport struct {
	Glass      string `json:"glass"`
	FullScreen bool   `json:"fullScreen"`
	// MenuBarVisible: the menu bar is shown, as a pointer at the top of the
	// screen shows it in full screen, the title bar coming out under it.
	MenuBarVisible bool      `json:"menuBarVisible"`
	ToolbarVisible bool      `json:"toolbarVisible"`
	Close          box       `json:"close"`
	Minimize       box       `json:"minimize"`
	Zoom           box       `json:"zoom"`
	Orchestrator   box       `json:"orchestrator"`
	Sessions       box       `json:"sessions"`
	Row            box       `json:"row"`
	Capsules       []capsule `json:"capsules"`
	// SegmentBorderShape is the tabs' NSControlBorderShape, -1 on a system
	// without one; SelectedTopInset where the selected tab's fill starts in
	// its top row, as a share of its height.
	SegmentBorderShape int     `json:"segmentBorderShape"`
	SelectedTopInset   float64 `json:"selectedTopInset"`
	// RoundedTopInset is the same measure of a rounded rectangle drawn in the
	// tabs' place.
	RoundedTopInset float64 `json:"roundedTopInset"`
	// ContentLayoutTop is how much of the window's top its title bar and
	// toolbar keep from the content; Overlays what lies over it there.
	ContentLayoutTop float64   `json:"contentLayoutTop"`
	Overlays         []overlay `json:"overlays"`
}

// headerReport is the orchestrator surface's word on its header, in points
// from the surface's top.
type headerReport struct {
	Surface         string  `json:"surface"`
	HeaderRowCenter float64 `json:"headerRowCenter"`
	BrandCenter     float64 `json:"brandCenter"`
	FullScreen      bool    `json:"fullscreen"`
}

// boardReport is the board's word on its box, in points from the window's left
// edge: the board's web view is the whole window. LastColumnRightAtEnd is where
// its last column ends at the end of its scroll, nil with no columns.
type boardReport struct {
	BoardLeft            float64  `json:"boardLeft"`
	BoardRight           float64  `json:"boardRight"`
	LastColumnRightAtEnd *float64 `json:"lastColumnRightAtEnd"`
}

// standLog is what the log says, in order: the frames, the header's reports,
// and the board's, each with the frame last measured before it (-1 for none).
type standLog struct {
	frames  []frameReport
	headers []headerReport
	boards  []boardAfter
}

type boardAfter struct {
	frame int
	boardReport
}

// run is the frames from index from to index to, all in full screen or all out
// of it.
type run struct {
	from, to   int
	fullScreen bool
}

func main() {
	logPath := flag.String("log", "", "the window's log")
	trips := flag.Int("fullscreen-trips", 0, "how many times the stand took the window into full screen and out (FLEETDECK_STAND_FULLSCREEN)")
	flag.Parse()
	raw, err := os.ReadFile(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "standcheck: %v\n", err)
		os.Exit(2)
	}
	problems := check(string(raw), *trips)
	for _, p := range problems {
		fmt.Println("frame: " + p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Println("frame: every property holds")
}

// check is what is wrong with the frame the log describes; nothing when the
// frame keeps every property. trips: how many times the stand took the window
// into full screen and out.
func check(log string, trips int) []string {
	l, problems := parse(log)
	if len(l.frames) == 0 {
		return append(problems, "no frame report in the window's log: the window never measured its frame")
	}
	runs := runsOf(l.frames)
	if runs[0].fullScreen {
		problems = append(problems, "no frame report out of full screen before it")
	}
	in, out := 0, 0
	for i, r := range runs {
		f := l.frames[r.to]
		if r.fullScreen {
			in++
			when := fmt.Sprintf("in full screen %d: ", in)
			problems = append(problems, capsuleProblems(when, f)...)
			problems = append(problems, tabProblems(when, f)...)
			problems = append(problems, l.boardProblems(when, r)...)
			problems = append(problems, coverProblems(when, f)...)
			// Every frame after the window settled, the menu bar hidden: the
			// stand brings the pointer to the top of the screen meanwhile, and
			// what that brings out over the content need not change anything
			// else the window says. The window goes in with the menu bar still
			// shown from before, under the transition's overlay, which is not
			// that. The first frame with a problem is enough.
			for _, k := range settledFrames(l.frames, r) {
				if p := coverProblems(fmt.Sprintf("in full screen %d, a frame after it settled: ", in), l.frames[k]); len(p) > 0 {
					problems = append(problems, p...)
					break
				}
			}
			continue
		}
		out++
		when := ""
		if i > 0 {
			when = fmt.Sprintf("after full screen %d: ", in)
		}
		problems = append(problems, capsuleProblems(when, f)...)
		problems = append(problems, tabProblems(when, f)...)
		problems = append(problems, l.boardProblems(when, r)...)
		problems = append(problems, buttonProblems(when, f)...)
		problems = append(problems, underButtonProblems(when, f)...)
		// A folded orchestrator strip shows no header, only its unfold mark,
		// with the buttons in the corner over it.
		const foldedStripWidth = 48
		if i == 0 && f.Orchestrator.W > foldedStripWidth+lineSlack {
			problems = append(problems, headerProblems(f, l.headers)...)
		}
	}
	switch {
	case trips == 0:
	case in == 0:
		problems = append(problems, "the window was asked for full screen and never entered full screen")
	case in < trips:
		problems = append(problems, fmt.Sprintf("the window was asked into full screen %d times and entered it %d", trips, in))
	}
	if trips > 0 && runs[len(runs)-1].fullScreen {
		problems = append(problems, "the window entered full screen and never left full screen")
	}
	return problems
}

func parse(log string) (standLog, []string) {
	var l standLog
	var problems []string
	for _, line := range strings.Split(log, "\n") {
		if i := strings.Index(line, framePrefix); i >= 0 {
			var f frameReport
			if err := json.Unmarshal([]byte(line[i+len(framePrefix):]), &f); err != nil {
				problems = append(problems, fmt.Sprintf("a frame report that is not one: %v", err))
				continue
			}
			l.frames = append(l.frames, f)
		}
		if i := strings.Index(line, headerPrefix); i >= 0 && strings.Contains(line, `"headerRowCenter"`) {
			var h headerReport
			if err := json.Unmarshal([]byte(line[i+len(headerPrefix):]), &h); err != nil {
				problems = append(problems, fmt.Sprintf("a header report that is not one: %v", err))
				continue
			}
			l.headers = append(l.headers, h)
		}
		if i := strings.Index(line, boardPrefix); i >= 0 {
			var b boardReport
			if err := json.Unmarshal([]byte(line[i+len(boardPrefix):]), &b); err != nil {
				problems = append(problems, fmt.Sprintf("a board report that is not one: %v", err))
				continue
			}
			l.boards = append(l.boards, boardAfter{frame: len(l.frames) - 1, boardReport: b})
		}
	}
	return l, problems
}

// runsOf splits frames into runs in full screen and out of it, in order.
func runsOf(frames []frameReport) []run {
	var runs []run
	for i, f := range frames {
		if len(runs) > 0 && runs[len(runs)-1].fullScreen == f.FullScreen {
			runs[len(runs)-1].to = i
			continue
		}
		runs = append(runs, run{from: i, to: i, fullScreen: f.FullScreen})
	}
	return runs
}

func (b box) span() string { return fmt.Sprintf("%v..%v", b.X, b.X+b.W) }

func overlaps(a, b box) bool {
	return a.X < b.X+b.W-edgeSlack && b.X < a.X+a.W-edgeSlack && a.Y < b.Y+b.H-edgeSlack && b.Y < a.Y+a.H-edgeSlack
}

func capsuleProblems(when string, f frameReport) []string {
	var out []string
	for _, c := range f.Capsules {
		if c.X < f.Row.X-edgeSlack || c.X+c.W > f.Row.X+f.Row.W+edgeSlack {
			out = append(out, fmt.Sprintf("%s%s at %s is outside the row %s", when, c.Name, c.span(), f.Row.span()))
		}
		for _, panel := range []struct {
			name string
			b    box
		}{{"orchestrator panel", f.Orchestrator}, {"sessions panel", f.Sessions}} {
			if overlaps(c.box, panel.b) {
				out = append(out, fmt.Sprintf("%s%s at %s lies over the %s at %s", when, c.Name, c.span(), panel.name, panel.b.span()))
			}
		}
	}
	return out
}

// coverProblems is what of the title bar keeps the window's top or lies shown
// over the capsule row, in full screen.
func coverProblems(when string, f frameReport) []string {
	var out []string
	if f.ContentLayoutTop > lineSlack {
		out = append(out, fmt.Sprintf("%sthe top %v pt of the window is kept by its title bar", when, f.ContentLayoutTop))
	}
	for _, o := range f.Overlays {
		if o.Visible && o.Alpha > clearAlpha && overlaps(o.box, f.Row) {
			out = append(out, fmt.Sprintf("%s%s at y %v..%v is shown over the capsule row at y %v..%v", when, o.Kind, o.Y, o.Y+o.H, f.Row.Y, f.Row.Y+f.Row.H))
		}
	}
	return out
}

// boardProblems holds the board's last report among the frames of r to r's last
// frame.
// settledFrames is the frames of r after the first with the menu bar hidden,
// short of r's last, which is held to the cover property on its own.
func settledFrames(frames []frameReport, r run) []int {
	var out []int
	settled := false
	for k := r.from; k < r.to; k++ {
		if settled {
			out = append(out, k)
		}
		settled = settled || !frames[k].MenuBarVisible
	}
	return out
}

func (l standLog) boardProblems(when string, r run) []string {
	f := l.frames[r.to]
	for k := len(l.boards) - 1; k >= 0; k-- {
		b := l.boards[k]
		// A report before the window first measured its frame is of the first
		// frame: the board's page can report first by a few milliseconds.
		if b.frame < 0 {
			b.frame = 0
		}
		if b.frame < r.from || b.frame > r.to {
			continue
		}
		var out []string
		sessions, orchestrator := f.Sessions.X, f.Orchestrator.X+f.Orchestrator.W
		if math.Abs(b.BoardRight-sessions) > lineSlack {
			out = append(out, fmt.Sprintf("%sthe board ends at %v, the sessions panel starts at %v", when, b.BoardRight, sessions))
		}
		if b.BoardLeft < orchestrator-lineSlack || b.BoardLeft > orchestrator+boardGapLeft+lineSlack {
			out = append(out, fmt.Sprintf("%sthe board starts at %v, want within %v pt past the orchestrator panel's edge at %v", when, b.BoardLeft, boardGapLeft, orchestrator))
		}
		if b.LastColumnRightAtEnd != nil && *b.LastColumnRightAtEnd > sessions+lineSlack {
			out = append(out, fmt.Sprintf("%sthe board's last column ends at %v at the end of its scroll, under the sessions panel from %v", when, *b.LastColumnRightAtEnd, sessions))
		}
		return out
	}
	return []string{when + "no board report beside the frame"}
}

// tabProblems holds the tabs to a capsule: their border shape, and the selected
// tab's fill against a rounded rectangle drawn in its place.
func tabProblems(when string, f frameReport) []string {
	if f.SegmentBorderShape < 0 {
		return nil
	}
	var out []string
	if f.SegmentBorderShape != 1 {
		out = append(out, fmt.Sprintf("%sthe tabs' border shape is %d, want 1 (a capsule)", when, f.SegmentBorderShape))
	}
	if f.RoundedTopInset <= 0 {
		out = append(out, when+"no rounded rectangle measured to hold the selected tab against")
	} else if f.SelectedTopInset < f.RoundedTopInset+capsuleOverRoundedInset {
		out = append(out, fmt.Sprintf("%sthe selected tab's fill starts %.2f of its height in at its top, a rounded rectangle's in its place %.2f: want a capsule's, at least %.2f further in", when, f.SelectedTopInset, f.RoundedTopInset, capsuleOverRoundedInset))
	}
	return out
}

// closeCentre is where the close button is centred, and whether one was measured.
func closeCentre(f frameReport) (float64, float64, bool) {
	return f.Close.X + f.Close.W/2, f.Close.Y + f.Close.H/2, f.Close.W != 0
}

func buttonProblems(when string, f frameReport) []string {
	cx, cy, ok := closeCentre(f)
	if !ok {
		return []string{when + "no close button measured out of full screen"}
	}
	wx, wy := f.Orchestrator.X+panelRadius, f.Orchestrator.Y+panelRadius
	if math.Abs(cx-wx) > lineSlack || math.Abs(cy-wy) > lineSlack {
		return []string{fmt.Sprintf("%sthe close button is centred at (%v, %v), want (%v, %v), concentric in the orchestrator panel's corner", when, cx, cy, wx, wy)}
	}
	return nil
}

// underButtonProblems is each capsule that lies under one of the window's
// buttons, out of full screen. v0.10.2's dev build laid the tabs under the zoom
// button beside the folded orchestrator strip (the operator's frame 1374).
func underButtonProblems(when string, f frameReport) []string {
	var out []string
	for _, c := range f.Capsules {
		for _, button := range []struct {
			name string
			b    box
		}{{"close", f.Close}, {"minimize", f.Minimize}, {"zoom", f.Zoom}} {
			if overlaps(c.box, button.b) {
				out = append(out, fmt.Sprintf("%s%s at %s lies under the window's %s button at %s", when, c.Name, c.span(), button.name, button.b.span()))
			}
		}
	}
	return out
}

func headerProblems(f frameReport, headers []headerReport) []string {
	_, cy, ok := closeCentre(f)
	if !ok {
		return nil
	}
	var header *headerReport
	for i := len(headers) - 1; i >= 0; i-- {
		if !headers[i].FullScreen {
			header = &headers[i]
			break
		}
	}
	if header == nil {
		return []string{"no header report from the orchestrator surface out of full screen"}
	}
	var out []string
	if row := f.Orchestrator.Y + header.HeaderRowCenter; math.Abs(row-cy) > lineSlack {
		out = append(out, fmt.Sprintf("the orchestrator's header row is centred %v pt down, the window's buttons %v", row, cy))
	}
	if brand := f.Orchestrator.Y + header.BrandCenter; math.Abs(brand-cy) > brandSlack {
		out = append(out, fmt.Sprintf("the brand is centred %v pt down, the window's buttons %v", brand, cy))
	}
	return out
}
