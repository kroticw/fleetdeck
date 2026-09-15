// Command standcheck reads the window's log from a CI stand
// (scripts/ci-window-stand.sh) and says whether the frame keeps what the
// operator saw broken in v0.10.1, by what the window measured of itself
// natively (cmd/fleetdeck-window/standframe_darwin.go) and what the pages
// measured of themselves (web/js/standheader.js, web/js/standreport.js):
//
//   - every capsule is inside the row and over neither panel, out of full
//     screen, in it, and after it;
//   - the board meets both panels as they are laid out, and its last column
//     comes out from under the sessions panel, in each of those;
//   - the selected tab is a capsule, where the system has border shapes;
//   - the window's buttons sit concentric in the orchestrator panel's corner,
//     and the header's row and its brand are centred on their line.
//
// The pages' own verdicts -- the board's lastColumnClear, worked out from the
// insets the page was sent -- are not read: in v0.10.1 they said true while the
// insets were stale. The board is held to the native panels' frames.
//
// Usage: standcheck -log <window.log> [-fullscreen]
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
	// A capsule's top row starts about half its height in, a rounded
	// rectangle's a fifth.
	minCapsuleTopInset = 0.35
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

// frameReport is the window's word on its frame, in points from the window's
// top left.
type frameReport struct {
	Glass        string    `json:"glass"`
	FullScreen   bool      `json:"fullScreen"`
	Close        box       `json:"close"`
	Orchestrator box       `json:"orchestrator"`
	Sessions     box       `json:"sessions"`
	Row          box       `json:"row"`
	Capsules     []capsule `json:"capsules"`
	// SegmentBorderShape is the tabs' NSControlBorderShape, -1 on a system
	// without one; SelectedTopInset where the selected tab's fill starts in
	// its top row, as a share of its height.
	SegmentBorderShape int     `json:"segmentBorderShape"`
	SelectedTopInset   float64 `json:"selectedTopInset"`
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

func main() {
	logPath := flag.String("log", "", "the window's log")
	fullScreen := flag.Bool("fullscreen", false, "the stand asked the window for full screen (FLEETDECK_STAND_FULLSCREEN)")
	flag.Parse()
	raw, err := os.ReadFile(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "standcheck: %v\n", err)
		os.Exit(2)
	}
	problems := check(string(raw), *fullScreen)
	for _, p := range problems {
		fmt.Println("frame: " + p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Println("frame: every property holds")
}

// check is what is wrong with the frame the log describes; nothing when the
// frame keeps every property. fullScreen: the stand asked for full screen.
func check(log string, fullScreen bool) []string {
	l, problems := parse(log)
	last := len(l.frames) - 1
	if last < 0 {
		return append(problems, "no frame report in the window's log: the window never measured its frame")
	}
	firstIn := -1
	for i, f := range l.frames {
		if f.FullScreen {
			firstIn = i
			break
		}
	}
	outTo := last
	if fullScreen && firstIn >= 0 {
		outTo = firstIn - 1
	}
	if i, ok := l.lastFrame(0, outTo, false); ok {
		f := l.frames[i]
		problems = append(problems, capsuleProblems("", f)...)
		problems = append(problems, tabProblems(f)...)
		problems = append(problems, buttonProblems(f, l.headers)...)
		problems = append(problems, l.boardProblems("", 0, outTo, false)...)
	} else {
		problems = append(problems, "no frame report out of full screen")
	}
	if !fullScreen {
		return problems
	}
	if firstIn < 0 {
		return append(problems, "the window was asked for full screen and never entered full screen")
	}
	in, _ := l.lastFrame(firstIn, last, true)
	problems = append(problems, capsuleProblems("in full screen: ", l.frames[in])...)
	problems = append(problems, l.boardProblems("in full screen: ", firstIn, last, true)...)
	if after, ok := l.lastFrame(firstIn, last, false); ok {
		problems = append(problems, capsuleProblems("after full screen: ", l.frames[after])...)
		problems = append(problems, l.boardProblems("after full screen: ", firstIn, last, false)...)
	} else {
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

// lastFrame is the last frame from index from to index to whose full screen is
// as asked.
func (l standLog) lastFrame(from, to int, fullScreen bool) (int, bool) {
	for i := to; i >= from && i >= 0; i-- {
		if l.frames[i].FullScreen == fullScreen {
			return i, true
		}
	}
	return 0, false
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

// boardProblems holds the board's last report among the frames from index from
// to index to whose full screen is as asked to the last of those frames.
func (l standLog) boardProblems(when string, from, to int, fullScreen bool) []string {
	i, ok := l.lastFrame(from, to, fullScreen)
	if !ok {
		return nil
	}
	f := l.frames[i]
	for k := len(l.boards) - 1; k >= 0; k-- {
		b := l.boards[k]
		if b.frame < from || b.frame > to || l.frames[b.frame].FullScreen != fullScreen {
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

func tabProblems(f frameReport) []string {
	if f.SegmentBorderShape < 0 {
		return nil
	}
	var out []string
	if f.SegmentBorderShape != 1 {
		out = append(out, fmt.Sprintf("the tabs' border shape is %d, want 1 (a capsule)", f.SegmentBorderShape))
	}
	if f.SelectedTopInset < minCapsuleTopInset {
		out = append(out, fmt.Sprintf("the selected tab's fill starts %.2f of its height in at its top, want a capsule's, about 0.5", f.SelectedTopInset))
	}
	return out
}

func buttonProblems(f frameReport, headers []headerReport) []string {
	if f.Close.W == 0 {
		return []string{"no close button measured out of full screen"}
	}
	var out []string
	cx, cy := f.Close.X+f.Close.W/2, f.Close.Y+f.Close.H/2
	wx, wy := f.Orchestrator.X+panelRadius, f.Orchestrator.Y+panelRadius
	if math.Abs(cx-wx) > lineSlack || math.Abs(cy-wy) > lineSlack {
		out = append(out, fmt.Sprintf("the close button is centred at (%v, %v), want (%v, %v), concentric in the orchestrator panel's corner", cx, cy, wx, wy))
	}
	var header *headerReport
	for i := len(headers) - 1; i >= 0; i-- {
		if !headers[i].FullScreen {
			header = &headers[i]
			break
		}
	}
	if header == nil {
		return append(out, "no header report from the orchestrator surface out of full screen")
	}
	if row := f.Orchestrator.Y + header.HeaderRowCenter; math.Abs(row-cy) > lineSlack {
		out = append(out, fmt.Sprintf("the orchestrator's header row is centred %v pt down, the window's buttons %v", row, cy))
	}
	if brand := f.Orchestrator.Y + header.BrandCenter; math.Abs(brand-cy) > brandSlack {
		out = append(out, fmt.Sprintf("the brand is centred %v pt down, the window's buttons %v", brand, cy))
	}
	return out
}
