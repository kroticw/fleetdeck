package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The frame of the macOS 26 stand's 1000 by 700 window, both panels unfolded
// and narrowed for the row, as the window measures it.
func goodFrame(fullScreen bool) frameReport {
	f := frameReport{
		Glass:              "glass",
		FullScreen:         fullScreen,
		Close:              box{X: 19, Y: 19, W: 14, H: 14},
		Orchestrator:       box{X: 8, Y: 8, W: 260, H: 684},
		Sessions:           box{X: 732, Y: 8, W: 260, H: 684},
		Row:                box{X: 278, Y: 10, W: 442, H: 32},
		SegmentBorderShape: 1,
		SelectedTopInset:   0.46,
		Capsules: []capsule{
			{Name: "tabs", box: box{X: 278, Y: 10, W: 123, H: 32}},
			{Name: "newCard", box: box{X: 409, Y: 10, W: 83, H: 32}},
			{Name: "compactLimits", box: box{X: 589, Y: 10, W: 131, H: 32}},
			{Name: "themeIcon", box: box{X: 540, Y: 10, W: 41, H: 32}},
		},
		// Out of full screen the transparent title bar and its toolbar keep
		// the top of the window, over the capsule row.
		ContentLayoutTop: 52,
		Overlays:         []overlay{{Kind: "NSTitlebarContainerView", box: box{W: 1000, H: 52}, Visible: true, Alpha: 1}},
	}
	if fullScreen {
		// The screen's width: the sessions panel and the row's right end move;
		// the title bar hides with the menu bar.
		f.Close = box{}
		f.Sessions.X, f.Row.W = 1460, 1170
		f.Capsules[2].X, f.Capsules[3].X = 1317, 1268
		f.ContentLayoutTop = 0
		f.Overlays = []overlay{{Kind: "NSTitlebarContainerView", box: box{W: 1024, H: 52}, Visible: false, Alpha: 1}}
	}
	return f
}

func goodHeader(fullScreen bool) headerReport {
	return headerReport{Surface: "orchestrator", HeaderRowCenter: 18, BrandCenter: 18.4, FullScreen: fullScreen}
}

// The board between goodFrame's panels: its box from 2 pt past the orchestrator
// panel to the sessions panel's edge, its last column out in the open at the
// end of its scroll.
func goodBoard(fullScreen bool) boardReport {
	b := boardReport{BoardLeft: 270, BoardRight: 732, LastColumnRightAtEnd: at(716)}
	if fullScreen {
		b.BoardRight, b.LastColumnRightAtEnd = 1460, at(1444)
	}
	return b
}

func at(x float64) *float64 { return &x }

// goodTrip is a trip into full screen and out: each frame, the board's report
// after it, and the header's.
func goodTrip() []any {
	return []any{goodFrame(true), goodBoard(true), goodHeader(true), goodFrame(false), goodBoard(false), goodHeader(false)}
}

// goodLog is a stand that went into full screen and out trips times.
func goodLog(t *testing.T, trips int) string {
	reports := []any{goodFrame(false), goodBoard(false), goodHeader(false)}
	for i := 0; i < trips; i++ {
		reports = append(reports, goodTrip()...)
	}
	return logOf(t, reports...)
}

func logOf(t *testing.T, reports ...any) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("2026/09/15 11:00:00.000000 fleetdeck-window: the panel's page says \"panel\"\n")
	for _, r := range reports {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		switch r.(type) {
		case frameReport:
			b.WriteString("2026/09/15 11:00:01.000000 fleetdeck-window: the frame measures " + string(raw) + "\n")
		case headerReport:
			b.WriteString("2026/09/15 11:00:01.000000 fleetdeck-window: the orchestrator surface reports its scrolling: " + string(raw) + "\n")
		case boardReport:
			b.WriteString("2026/09/15 11:00:01.000000 fleetdeck-window: the board reports its scrolling: " + string(raw) + "\n")
		}
	}
	// The terminal's report comes through the same line and says nothing of
	// the header.
	b.WriteString(`2026/09/15 11:00:02.000000 fleetdeck-window: the orchestrator surface reports its scrolling: {"surface":"orchestrator","viewportScrollbarWidth":0,"ownBarOpacity":0}` + "\n")
	return b.String()
}

func wantProblem(t *testing.T, problems []string, words ...string) {
	t.Helper()
	for _, p := range problems {
		all := true
		for _, w := range words {
			all = all && strings.Contains(p, w)
		}
		if all {
			return
		}
	}
	t.Fatalf("problems %q, want one naming %q", problems, words)
}

func TestAFrameThatKeepsItsPropertiesIsNoProblem(t *testing.T) {
	if got := check(goodLog(t, 0), 0); len(got) != 0 {
		t.Fatalf("problems %q, want none", got)
	}
	if got := check(goodLog(t, 2), 2); len(got) != 0 {
		t.Fatalf("in and out of full screen twice: problems %q, want none", got)
	}
}

// v0.10.1 in full screen on the operator's macOS: the theme and the limits over
// the sessions panel, outside the row.
func TestACapsuleOverTheSessionsPanelInFullScreenIsAProblem(t *testing.T) {
	fs := goodFrame(true)
	fs.Capsules[3].X, fs.Capsules[2].X = 1480, 1530
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), fs, goodBoard(true), goodFrame(false), goodBoard(false))
	problems := check(log, 1)
	wantProblem(t, problems, "in full screen", "themeIcon", "sessions panel")
	wantProblem(t, problems, "in full screen", "compactLimits", "outside the row")
}

func TestACapsuleOverTheOrchestratorPanelIsAProblem(t *testing.T) {
	f := goodFrame(false)
	f.Capsules[0].X = 250
	wantProblem(t, check(logOf(t, f, goodBoard(false), goodHeader(false)), 0), "tabs", "orchestrator panel")
}

// v0.10.1 out of full screen: the buttons centred 16 pt in, on the panel's edge.
func TestButtonsOnThePanelsEdgeAreAProblem(t *testing.T) {
	f := goodFrame(false)
	f.Close = box{X: 9, Y: 9, W: 14, H: 14}
	h := goodHeader(false)
	h.HeaderRowCenter, h.BrandCenter = 8, 8
	wantProblem(t, check(logOf(t, f, goodBoard(false), h), 0), "close button", "corner")
}

// Coming out of full screen must put the buttons back where they were.
func TestButtonsOffTheirPlaceAfterFullScreenAreAProblem(t *testing.T) {
	after := goodFrame(false)
	after.Close = box{X: 9, Y: 9, W: 14, H: 14}
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), goodFrame(true), goodBoard(true), after, goodBoard(false))
	wantProblem(t, check(log, 1), "after full screen", "close button")
}

// v0.10.1: the brand and the fleet picker sat lower than the buttons.
func TestAHeaderRowOffTheButtonsLineIsAProblem(t *testing.T) {
	h := goodHeader(false)
	h.HeaderRowCenter, h.BrandCenter = 22, 22
	problems := check(logOf(t, goodFrame(false), goodBoard(false), h), 0)
	wantProblem(t, problems, "header row")
	wantProblem(t, problems, "brand")
}

func TestAHeaderInFullScreenIsNotHeldToButtonsItDoesNotHave(t *testing.T) {
	fsHeader := goodHeader(true)
	fsHeader.HeaderRowCenter, fsHeader.BrandCenter = 30, 30
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), goodFrame(true), goodBoard(true), fsHeader, goodFrame(false), goodBoard(false))
	if got := check(log, 1); len(got) != 0 {
		t.Fatalf("problems %q, want none: in full screen there are no buttons", got)
	}
}

func TestNoHeaderReportIsAProblem(t *testing.T) {
	wantProblem(t, check(logOf(t, goodFrame(false), goodBoard(false)), 0), "header")
}

// v0.10.1: the selected tab a rounded rectangle in its capsule.
func TestARoundedSelectedTabIsAProblem(t *testing.T) {
	f := goodFrame(false)
	f.SegmentBorderShape, f.SelectedTopInset = 0, 0.19
	problems := check(logOf(t, f, goodBoard(false), goodHeader(false)), 0)
	wantProblem(t, problems, "border shape")
	wantProblem(t, problems, "selected tab")
}

// Before macOS 26 a segmented control has no border shape, and its selected
// segment is a rounded rectangle as it always was.
func TestASystemWithoutBorderShapesIsNotHeldToACapsule(t *testing.T) {
	f := goodFrame(false)
	f.SegmentBorderShape, f.SelectedTopInset = -1, 0.19
	if got := check(logOf(t, f, goodBoard(false), goodHeader(false)), 0); len(got) != 0 {
		t.Fatalf("problems %q, want none", got)
	}
}

func TestAStandAskedForFullScreenThatNeverEnteredOrLeftItIsAProblem(t *testing.T) {
	wantProblem(t, check(goodLog(t, 0), 1), "never", "full screen")
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), goodFrame(true), goodBoard(true))
	wantProblem(t, check(log, 1), "never", "left full screen")
	wantProblem(t, check(goodLog(t, 1), 2), "full screen 2 times", "entered it 1")
}

func TestNoFrameReportIsAProblem(t *testing.T) {
	wantProblem(t, check(logOf(t, goodHeader(false)), 0), "no frame")
}

// v0.10.1's stands, all six alike (run 34930674108): the panels narrowed for the
// row, the board kept the insets of panels at their widths -- its box from 378
// to 644, the sessions panel's edge the page worked out from those insets --
// while the native panels ended at 304.7 and started at 705.7. The page's own
// lastColumnClear said true.
func TestABoardHeldToInsetsTheNativePanelsDoNotHaveIsAProblem(t *testing.T) {
	f := goodFrame(false)
	f.Orchestrator.W, f.Sessions.X, f.Sessions.W = 296.7, 705.7, 286.3
	f.Row = box{X: 314.7, Y: 10, W: 379, H: 32}
	f.Capsules = nil
	board := boardReport{BoardLeft: 378, BoardRight: 644, LastColumnRightAtEnd: at(628)}
	problems := check(logOf(t, f, board, goodHeader(false)), 0)
	wantProblem(t, problems, "board ends at 644", "sessions panel starts at 705.7")
	wantProblem(t, problems, "board starts at 378", "orchestrator panel")
}

func TestALastColumnLeftUnderTheSessionsPanelIsAProblem(t *testing.T) {
	board := goodBoard(true)
	board.LastColumnRightAtEnd = at(1500)
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), goodFrame(true), board, goodFrame(false), goodBoard(false))
	wantProblem(t, check(log, 1), "in full screen", "last column", "sessions panel")
}

// A board report from before full screen does not stand for the board in it.
func TestABoardNeverReportedInAPhaseIsAProblem(t *testing.T) {
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), goodFrame(true), goodFrame(false), goodBoard(false))
	wantProblem(t, check(log, 1), "in full screen", "no board report")
	wantProblem(t, check(logOf(t, goodFrame(false), goodHeader(false)), 0), "no board report")
}

// The board's report after a frame stands for the board beside the frames that
// follow it until the board says otherwise, and a board that said nothing after
// the frame changed is held to the frame as it last was.
func TestABoardIsHeldToTheLastFrameOfItsPhase(t *testing.T) {
	later := goodFrame(false)
	later.Sessions.X, later.Sessions.W = 700, 292
	log := logOf(t, goodFrame(false), goodBoard(false), later, goodHeader(false))
	wantProblem(t, check(log, 0), "board ends at 732", "sessions panel starts at 700")
}

// v0.10.2's first full screen stand (run 34932941637): the empty toolbar stayed
// in full screen as a black band 64 pt tall over the capsule row, the
// orchestrator's brand row and the sessions island's head. The native frames
// were right, and the checker passed.
func TestATitleBarKeepingTheTopInFullScreenIsAProblem(t *testing.T) {
	fs := goodFrame(true)
	fs.ContentLayoutTop = 64
	log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), fs, goodBoard(true), goodFrame(false), goodBoard(false))
	wantProblem(t, check(log, 1), "in full screen", "top 64 pt", "title bar")
}

func TestAShownTitleBarOverTheCapsuleRowInFullScreenIsAProblem(t *testing.T) {
	for _, over := range []overlay{
		{Kind: "NSTitlebarContainerView", box: box{W: 1024, H: 64}, Visible: true, Alpha: 1},
		{Kind: "NSToolbarFullScreenWindow", box: box{W: 1024, H: 64}, Visible: true, Alpha: 1},
	} {
		fs := goodFrame(true)
		fs.Overlays = []overlay{over}
		log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), fs, goodBoard(true), goodFrame(false), goodBoard(false))
		wantProblem(t, check(log, 1), "in full screen", over.Kind, "capsule row")
	}
}

// A title bar hidden, clear, above the screen or out of full screen covers
// nothing of the capsule row a person sees.
func TestATitleBarThatShowsNothingOverTheRowIsNoProblem(t *testing.T) {
	for _, over := range []overlay{
		{Kind: "NSTitlebarContainerView", box: box{W: 1024, H: 64}, Visible: false, Alpha: 1},
		{Kind: "NSToolbarFullScreenWindow", box: box{W: 1024, H: 64}, Visible: true, Alpha: 0},
		{Kind: "NSToolbarFullScreenWindow", box: box{Y: -64, W: 1024, H: 64}, Visible: true, Alpha: 1},
	} {
		fs := goodFrame(true)
		fs.Overlays = []overlay{over}
		log := logOf(t, goodFrame(false), goodBoard(false), goodHeader(false), fs, goodBoard(true), goodFrame(false), goodBoard(false))
		if got := check(log, 1); len(got) != 0 {
			t.Errorf("%+v: problems %q, want none", over, got)
		}
	}
}
