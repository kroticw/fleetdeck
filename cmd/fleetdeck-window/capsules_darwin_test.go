package main

// The capsules are drawn once, on the main thread, in TestMain
// (menu_darwin_test.go), on a window never put on screen; the tests below only
// read what was found.

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
)

var (
	capsulesResult capsulesProbe
	themeResult    capsuleThemeProbe
)

func collectCapsuleResults() {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,"tabs":[{"id":"board","label":"Доска","selected":true},{"id":"docs","label":"Доки","selected":false}],"newCard":{"label":"+ карточка"},"theme":{"label":"тема: авто"},"limits":[{"label":"5ч","text":"37%","level":"cool","color":"#2f9e44"}]}`))
	if err != nil {
		panic(err)
	}
	capsulesResult = probeCapsulesForTest(m)
	collectCapsuleLayoutResults()
	themeResult = probeCapsuleThemeForTest(m)
}

// --- the capsules and the app's theme ----------------------------------------------

// Every capsule takes the theme chosen in fleetdeck, whatever the system's. On
// the macOS 26 stand with real glass (run 34868250061) a capsule's glass took
// its tint from the board under it, and controls drawn in the system's mode
// were dark on dark glass or light on light: its controls keep the app's
// appearance. An opaque capsule's background is resolved in the app's
// appearance: resolved in the system's, it was white under white text with the
// app dark and the system light (runs 34863293838 and 34864709919, taken while
// the runner reduced transparency).

func TestCapsulesOnGlassKeepTheAppsAppearanceForTheirControls(t *testing.T) {
	if !themeResult.glass {
		t.Skip("NSGlassEffectView is not on this system: the capsules are not on glass")
	}
	if len(themeResult.onGlassInDarkApp) == 0 {
		t.Fatal("no capsules drawn")
	}
	for i, name := range themeResult.onGlassInDarkApp {
		if name != "" {
			t.Errorf("capsule %d's content has an appearance of its own, %q, instead of the app's", i, name)
		}
	}
}

func TestOpaqueCapsulesTakeTheirBackgroundFromTheAppsTheme(t *testing.T) {
	for _, c := range []struct {
		theme  string
		got    []float64
		darker bool
	}{{"dark", themeResult.opaqueInDarkApp, true}, {"light", themeResult.opaqueInLightApp, false}} {
		if len(c.got) == 0 {
			t.Fatalf("app %s: no capsules drawn", c.theme)
		}
		for i, b := range c.got {
			if b < 0 || (b < 0.5) != c.darker {
				t.Errorf("app %s: capsule %d's background is %.2f light, want it %s", c.theme, i, b, map[bool]string{true: "dark", false: "light"}[c.darker])
			}
		}
	}
}

func TestTheThemeProbeLeavesTheAppsAppearanceAsItFoundIt(t *testing.T) {
	if themeResult.appAppearanceAt != themeResult.appAppearanceBefore {
		t.Fatalf("app appearance %q after the probe, %q before", themeResult.appAppearanceAt, themeResult.appAppearanceBefore)
	}
}

func TestCapsulesDrawThePagesModel(t *testing.T) {
	r := capsulesResult
	if r.segmentLabels != [2]string{"Доска", "Доки"} || r.selectedSegment != 0 {
		t.Fatalf("segments = %v selected %d", r.segmentLabels, r.selectedSegment)
	}
	if r.newCardTitle != "+ карточка" || r.themeTitle != "тема: авто" {
		t.Fatalf("buttons = %q %q", r.newCardTitle, r.themeTitle)
	}
	if r.levelValue != 37 {
		t.Fatalf("level = %v", r.levelValue)
	}
	if r.capsuleCount != 4 {
		t.Fatalf("capsules = %d, want tabs, new card, theme, one limit", r.capsuleCount)
	}
}

// v0.10.1 on the operator's macOS: the selected tab was a rounded rectangle in
// its round capsule. From macOS 26 a segmented control has a border shape, and
// left automatic it draws a rounded rectangle at this size. Asked for a capsule,
// its selected segment is one too, and with the same room on every side the two
// capsules share their centres of curvature.
// capsuleOverRoundedInset is how much further in a capsule's top row starts than
// a rounded rectangle's, at the least: 0.46 against 0.19 on a 2x screen.
const capsuleOverRoundedInset = 0.05

func TestTheSelectedTabIsACapsuleInsideItsCapsule(t *testing.T) {
	r := capsulesResult
	if r.segmentBorderShape < 0 {
		t.Skip("NSControl.borderShape is not on this system (before macOS 26)")
	}
	if r.segmentBorderShape != 1 {
		t.Errorf("the tabs' border shape is %d, want 1 (NSControlBorderShapeCapsule)", r.segmentBorderShape)
	}
	if r.roundedTopInset <= 0 || r.selectedTopInset < r.roundedTopInset+capsuleOverRoundedInset {
		t.Errorf("the selected segment's fill starts %.2f of its height in at its top, a rounded rectangle's drawn in its place %.2f: want a capsule's, at least %.2f further in", r.selectedTopInset, r.roundedTopInset, capsuleOverRoundedInset)
	}
}

func TestTheTabsHaveTheSameRoomOnEverySideOfTheirCapsule(t *testing.T) {
	r := capsulesResult
	if r.segmentBorderShape < 0 {
		t.Skip("NSControl.borderShape is not on this system (before macOS 26): the tabs are no capsule to keep concentric")
	}
	in := r.tabsInsets
	for i, side := range []string{"right", "top", "bottom"} {
		if math.Abs(in[i+1]-in[0]) > 0.5 {
			t.Errorf("the tabs have %v pt of room on the left and %v on the %s, want the same", in[0], in[i+1], side)
		}
	}
}

func TestADrawnAgainRowReplacesTheCapsulesInsteadOfAddingThem(t *testing.T) {
	if capsulesResult.countAfterRedraw != 4 {
		t.Fatalf("after a second draw: %d, want the same four capsules in one row", capsulesResult.countAfterRedraw)
	}
}

func TestAPressInTheRowBecomesTheControllersAction(t *testing.T) {
	if !reflect.DeepEqual(capsulesResult.presses, []string{"tab:docs", "newCard"}) {
		t.Fatalf("presses = %v", capsulesResult.presses)
	}
}

// v0.10.0 drew the capsules' labels under the glass on the operator's macOS 26:
// the controls lay beside their glass in the row's NSGlassEffectContainerView,
// which draws its glass in a view above its content. Only a glass's contentView
// is drawn inside the glass.
func TestEachCapsuleControlOnGlassIsItsGlassesContent(t *testing.T) {
	if !frameResult.glassAvailable {
		t.Skip("NSGlassEffectView is not on this system: the capsules are not on glass")
	}
	for which, name := range []string{"the tabs", "the new card button", "the theme button"} {
		if !capsulesResult.insideGlass[which] {
			t.Errorf("%s, drawn on glass, is not its glass's content view: the glass draws over it", name)
		}
	}
}

// A click at the middle of each capsule reaches its control, on a window laid
// out as one on screen is: before layout a glass's content view has no size,
// and a hit test stops at the glass -- the measurement that once moved the
// controls out of the glass.
func TestAClickOnEachCapsuleOnGlassReachesItsControl(t *testing.T) {
	if !frameResult.glassAvailable {
		t.Skip("NSGlassEffectView is not on this system: the capsules are not on glass")
	}
	for which, name := range []string{"the tabs", "the new card button", "the theme button"} {
		if !capsulesResult.clicksReach[which] {
			t.Errorf("a click at the middle of %s, drawn on glass, does not reach it", name)
		}
	}
}

func TestAClickInTheGapBetweenCapsulesReachesTheBoard(t *testing.T) {
	if !capsulesResult.rowPassesThrough {
		t.Fatal("the capsule row's container takes a click between capsules, over the board")
	}
}

// --- a row in a narrow window ----------------------------------------------------

// v0.10.0 in a 1000 pt window with both panels unfolded: the row was about 246
// pt, the tabs and the new card button alone about 190, and the capsules from
// the right edge ran over them -- white empty pills in the dark.

// v0.10.1's first stand, 1000 by 700 with both panels unfolded: the board's
// layout gave the frame its geometry after the surfaces were created, and so
// after the row was drawn and had the panels narrowed for it -- the geometry
// computed before, with no row, put the panels back and the row at 246 pt.
// Read after the layout, as the stand's screenshot is, and again after a
// resize, which would have hidden it.
func TestOnAStandsPathThePanelsNarrowAndNoCapsuleOverlaps(t *testing.T) {
	for _, s := range []struct {
		when string
		f    standFrame
	}{
		{"after the board's layout", standResult.afterLayout},
		{"after the window's resize", standResult.afterResize},
		{"after the frame came up again", standResult.afterReframe},
		{"after longer labels", standResult.afterLongerLabels},
	} {
		if s.f.rowMin <= 0 {
			t.Errorf("%s: the controller has no row minimum", s.when)
			continue
		}
		if s.f.orchestrator >= 368 || s.f.sessions >= 348 {
			t.Errorf("%s: panels %v and %v, want both narrowed for the row", s.when, s.f.orchestrator, s.f.sessions)
		}
		if s.f.row < s.f.rowMin-0.01 {
			t.Errorf("%s: row %v, want its minimum %v", s.when, s.f.row, s.f.rowMin)
		}
		for _, problem := range overlapping(s.f.capsules, s.f.row) {
			t.Errorf("%s: %s", s.when, problem)
		}
	}
	if longer, before := standResult.afterLongerLabels.rowMin, standResult.afterReframe.rowMin; longer <= before {
		t.Errorf("row minimum %v after longer labels, %v before: want the row measured again", longer, before)
	}
}

// v0.10.1's stands, all six alike (run 34930674108): the panels narrowed for the
// capsule row, and the board kept the insets of panels at their widths -- 394
// on the left, 356 on the right -- and ended 61 pt short of the sessions glass.
// At the start the board heard the insets decided before the row was drawn
// after the ones decided after it. After every stage the last insets the board
// heard are those of the native panels as laid out.
func TestOnAStandsPathTheBoardsLastInsetsMeetTheNativePanelsAfterEveryStage(t *testing.T) {
	for _, s := range []struct {
		when string
		f    standFrame
	}{
		{"after the board's layout", standResult.afterLayout},
		{"after the window's resize", standResult.afterResize},
		{"after the frame came up again", standResult.afterReframe},
		{"after longer labels", standResult.afterLongerLabels},
		{"in full screen", standResult.afterFullScreen},
		{"after a drag on the sessions panel's edge was let go", standResult.afterDragRelease},
		{"with the sessions panel folded", standResult.afterFold},
	} {
		if !s.f.insetsSent {
			t.Errorf("%s: the board was sent no insets", s.when)
			continue
		}
		if want := s.f.orchestratorRight + boardGapLeft; math.Abs(s.f.insetsLeft-want) > 1 {
			t.Errorf("%s: the board's left inset is %v, want %v, %v past the orchestrator panel's edge at %v", s.when, s.f.insetsLeft, want, boardGapLeft, s.f.orchestratorRight)
		}
		if want := s.f.width - s.f.sessionsLeft; math.Abs(s.f.insetsContentRight-want) > 1 {
			t.Errorf("%s: the board's right inset is %v, want %v, up to the sessions panel's edge at %v of %v", s.when, s.f.insetsContentRight, want, s.f.sessionsLeft, s.f.width)
		}
	}
}

// overlapping says which shown capsules lie outside a row rowWidth wide or over
// one another.
func overlapping(capsules []drawnCapsule, rowWidth float64) []string {
	const slack = 0.01
	var shown []drawnCapsule
	for _, c := range capsules {
		if c.visible {
			shown = append(shown, c)
		}
	}
	var out []string
	for i, a := range shown {
		if a.x < -slack || a.x+a.w > rowWidth+slack {
			out = append(out, fmt.Sprintf("%s at %v..%v is outside the row %v", a.name, a.x, a.x+a.w, rowWidth))
		}
		for _, b := range shown[i+1:] {
			if a.x < b.x+b.w-slack && b.x < a.x+a.w-slack {
				out = append(out, fmt.Sprintf("%s at %v..%v overlaps %s at %v..%v", a.name, a.x, a.x+a.w, b.name, b.x, b.x+b.w))
			}
		}
	}
	return out
}

var (
	standResult   capsuleStandProbe
	layoutModel   capsuleModel
	layoutResults []capsuleLayoutProbe
	// resizedResult: drawn at 1440, then the window made 1000 with both panels
	// unfolded, the capsules not drawn again -- how a window is resized.
	resizedResult capsuleLayoutProbe
)

func collectCapsuleLayoutResults() {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,"tabs":[{"id":"board","label":"Доска","selected":true},{"id":"docs","label":"Доки","selected":false}],"newCard":{"label":"+ карточка"},"theme":{"label":"тема: авто"},"limits":[{"label":"5ч","text":"42%","level":"cool","color":"#2f9e44"},{"label":"7д","text":"91%","level":"hot","color":"#e03131"}]}`))
	if err != nil {
		panic(err)
	}
	layoutModel = m
	for _, width := range []float64{1440, 1000, 800} {
		for _, folded := range []bool{false, true} {
			layoutResults = append(layoutResults, probeCapsuleLayoutForTest(m, width, 0, folded))
		}
	}
	resizedResult = probeCapsuleLayoutForTest(m, 1000, 1440, false)
	standResult = probeCapsuleStandForTest(m)
	foldResults = probeFoldsForTest(m)
	regrowResults = []capsuleRegrowProbe{probeCapsuleRegrowForTest(m, 1000, 1728), probeCapsuleRegrowForTest(m, 1728, 1000)}
}

// regrowResults: the row drawn at one width and laid out at a much wider or
// narrower one without a redraw, as entering and leaving full screen do.
var regrowResults []capsuleRegrowProbe

// foldResults: a stand's window in every fold of its panels (probeFoldsForTest).
var foldResults []foldedStandFrame

// v0.10.2's dev build on macOS 27 (the operator's frame 1374): with the
// orchestrator panel folded to its strip, the capsule row began 10 pt past the
// strip, and the Board/Docs tabs lay under the window's buttons, which reach
// past it. In every fold no capsule shown lies under the close, minimize or
// zoom button.
func TestNoCapsuleLiesUnderTheWindowsButtonsInAnyFold(t *testing.T) {
	if len(foldResults) != 5 {
		t.Fatalf("%d folds measured, want 5", len(foldResults))
	}
	for _, r := range foldResults {
		f := r.frame
		buttons := []struct {
			name string
			b    measuredBox
		}{{"close", f.Close}, {"minimize", f.Minimize}, {"zoom", f.Zoom}}
		for _, button := range buttons {
			if button.b.W == 0 {
				t.Errorf("%s: no %s button measured", r.when, button.name)
			}
		}
		if len(f.Capsules) == 0 {
			t.Errorf("%s: no capsule shown", r.when)
		}
		for _, c := range f.Capsules {
			for _, button := range buttons {
				if boxesOverlap(c.measuredBox, button.b) {
					t.Errorf("%s: %s at %v..%v lies under the %s button at %v..%v", r.when, c.Name, c.X, c.X+c.W, button.name, button.b.X, button.b.X+button.b.W)
				}
			}
		}
	}
}

func boxesOverlap(a, b measuredBox) bool {
	const slack = 0.01
	return a.X < b.X+b.W-slack && b.X < a.X+a.W-slack && a.Y < b.Y+b.H-slack && b.Y < a.Y+a.H-slack
}

// v0.10.1 on the operator's macOS, in full screen: the theme and the limits lay
// over the sessions panel until a panel's edge was dragged. The row's content
// view grows in the window's layout pass, after the row has placed its capsules
// for the new width, and a capsule kept at the row's right edge by its
// autoresizing moved by that width a second time. A live resize moves the row a
// point or two at a time and hid it; full screen moves it by hundreds.
func TestARowLaidOutMuchWiderOrNarrowerKeepsItsCapsulesInsideIt(t *testing.T) {
	if len(regrowResults) == 0 {
		t.Fatal("no rows laid out again")
	}
	for _, p := range regrowResults {
		for _, problem := range overlapping(p.capsules, p.rowWidth) {
			t.Errorf("%v: %s", p, problem)
		}
		right := 0.0
		for _, c := range p.capsules {
			if c.visible && c.x+c.w > right {
				right = c.x + c.w
			}
		}
		if math.Abs(right-p.rowWidth) > 0.01 {
			t.Errorf("%v: the rightmost capsule ends at %v, want at the row's right edge", p, right)
		}
	}
}

func allLayouts() []capsuleLayoutProbe {
	return append(append([]capsuleLayoutProbe{}, layoutResults...), resizedResult)
}

func (p capsuleLayoutProbe) String() string {
	return fmt.Sprintf("window %v (drawn at %v), folded %v, row %v", p.width, p.drawnAt, p.folded, p.rowWidth)
}

func (p capsuleLayoutProbe) named(name string) []drawnCapsule {
	var out []drawnCapsule
	for _, c := range p.capsules {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

func (p capsuleLayoutProbe) one(t *testing.T, name string) drawnCapsule {
	t.Helper()
	found := p.named(name)
	if len(found) != 1 {
		t.Fatalf("%v: %d capsules named %s in %+v", p, len(found), name, p.capsules)
	}
	return found[0]
}

// stage is the form the row is in: 0 every limit and the theme's label, 1 the
// compact limits and the label, 2 the compact limits and the icon; -1 for any
// other mix.
func (p capsuleLayoutProbe) stage(t *testing.T) int {
	t.Helper()
	limits := p.named("limit")
	full := len(limits) > 0
	for _, l := range limits {
		full = full && l.visible
	}
	compact, label, icon := p.one(t, "compactLimits").visible, p.one(t, "theme").visible, p.one(t, "themeIcon").visible
	switch {
	case full && !compact && label && !icon:
		return 0
	case !full && compact && label && !icon:
		return 1
	case !full && compact && !label && icon:
		return 2
	}
	return -1
}

// Below the window's minimum even the narrowest row does not fit, with nothing
// left to take away: a window narrower than it grows to it once the row is
// drawn, as far as its screen allows.
func TestAWindowNarrowerThanItsMinimumGrowsToIt(t *testing.T) {
	for _, p := range layoutResults {
		want := math.Max(p.width, p.minContentWidth)
		if p.contentWidth < want && p.contentWidth > p.width {
			t.Logf("%v: grown to %v, the screen's width, short of %v", p, p.contentWidth, want)
			continue
		}
		if p.contentWidth != want {
			t.Errorf("%v: content %v wide after the draw, want %v", p, p.contentWidth, want)
		}
	}
	if p := layoutResults[4]; p.minContentWidth <= 800 {
		t.Errorf("%v: window minimum %v, want the 800 pt window with both panels unfolded below it", p, p.minContentWidth)
	}
}

func TestNoTwoCapsulesInTheRowOverlapAndAllAreInsideIt(t *testing.T) {
	for _, p := range allLayouts() {
		if p.laidWidth < p.minContentWidth {
			t.Logf("%v: a screen narrower than the window's minimum %v", p, p.minContentWidth)
			continue
		}
		for _, problem := range overlapping(p.capsules, p.rowWidth) {
			t.Errorf("%v: %s", p, problem)
		}
	}
}

func TestTheTabsAndTheNewCardButtonAreAlwaysWhole(t *testing.T) {
	wide := layoutResults[1] // 1440, folded: room for everything
	for _, p := range allLayouts() {
		for _, name := range []string{"tabs", "newCard"} {
			c, want := p.one(t, name), wide.one(t, name)
			if !c.visible || c.w != want.w {
				t.Errorf("%v: %s visible %v, %v wide; want shown at its %v", p, name, c.visible, c.w, want.w)
			}
		}
	}
}

func TestTheLimitsAreShownAtEveryWidthInOneFormOrTheOther(t *testing.T) {
	for _, p := range allLayouts() {
		if s := p.stage(t); s < 0 {
			t.Errorf("%v: limits and theme in no form: %+v", p, p.capsules)
		}
	}
}

// The limits compact first, the theme after: never the theme's icon beside
// every limit, and a narrower window never in a wider form.
func TestANarrowerWindowCompactsTheLimitsBeforeTheTheme(t *testing.T) {
	for _, folded := range []bool{false, true} {
		last := -1
		for _, p := range layoutResults {
			if p.folded != folded {
				continue
			}
			s := p.stage(t)
			if s < last {
				t.Errorf("%v: form %d, wider than the wider window's %d", p, s, last)
			}
			last = s
		}
	}
	if s := layoutResults[1].stage(t); s != 0 {
		t.Errorf("%v: form %d, want every limit with room for it", layoutResults[1], s)
	}
	// The window v0.10.0 broke in: no room for the indicators.
	if s := layoutResults[2].stage(t); s == 0 {
		t.Errorf("%v: every limit shown in a row with no room for them", layoutResults[2])
	}
}

func TestAWindowResizedWithoutARedrawTakesTheFormOfItsWidth(t *testing.T) {
	if got, want := resizedResult.stage(t), layoutResults[2].stage(t); got != want {
		t.Fatalf("%v: form %d, want %d as drawn at that width", resizedResult, got, want)
	}
}

// The window's minimum content width is the frame around the row -- both
// panels at their readable width, margins and gaps -- and the row's narrowest
// form: the tabs, the new card button, the compact limits and the theme's icon,
// with the row's 8 pt gaps between them.
func TestTheWindowsMinimumWidthIsTheFrameAndTheNarrowestRow(t *testing.T) {
	for _, p := range allLayouts() {
		parts := p.one(t, "tabs").w + 8 + p.one(t, "newCard").w + 8 + p.one(t, "compactLimits").w + 8 + p.one(t, "themeIcon").w
		if p.rowMin != parts {
			t.Errorf("%v: row minimum %v, want %v", p, p.rowMin, parts)
		}
		if want := frameMinWidth() + parts; p.minContentWidth != want {
			t.Errorf("%v: window minimum %v, want %v", p, p.minContentWidth, want)
		}
		if p.minAfterClear != 0 {
			t.Errorf("%v: minimum after the row is cleared = %v, want none", p, p.minAfterClear)
		}
	}
}

func TestTheFrameKeepsTheRowItsMinimumWhereThePanelsCanNarrow(t *testing.T) {
	for _, p := range allLayouts() {
		if p.geometry.Orchestrator.W > minPanelWidth || p.folded {
			if p.rowWidth < p.rowMin-0.01 {
				t.Errorf("%v: row %v, narrower than its minimum %v with panels %v and %v", p, p.rowWidth, p.rowMin, p.geometry.Orchestrator.W, p.geometry.Sessions.W)
			}
		}
	}
}

func TestTheCompactLimitsCapsuleIsTheModelsLine(t *testing.T) {
	p, want := layoutResults[2], layoutModel.compactLimits()
	if p.compactText != want.Text || p.compactTooltip != want.Tooltip {
		t.Fatalf("compact = %q, tooltip %q; want %q, %q", p.compactText, p.compactTooltip, want.Text, want.Tooltip)
	}
}

func TestTheThemesIconSaysTheModeAndCyclesItAsTheLabelDoes(t *testing.T) {
	p := layoutResults[2]
	if p.themeIconTitle != "◐" {
		t.Fatalf("icon = %q", p.themeIconTitle)
	}
	if p.themeIconTooltip != "тема: авто" || p.themeIconAccessible != "тема: авто" {
		t.Fatalf("tooltip %q, accessibility label %q; want the theme's label", p.themeIconTooltip, p.themeIconAccessible)
	}
	if !reflect.DeepEqual(p.iconPresses, []string{"theme"}) {
		t.Fatalf("presses = %v", p.iconPresses)
	}
}
