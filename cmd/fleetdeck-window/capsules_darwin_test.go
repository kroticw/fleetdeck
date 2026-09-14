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
	observeSystemThemeLocallyForTest()
	capsulesResult = probeCapsulesForTest(m)
	collectCapsuleLayoutResults()
	themeResult = probeCapsuleThemeForTest(m)
}

// --- the capsules' controls and the system's mode --------------------------------

// On macOS 26 a capsule's glass takes the system's mode whatever the app's
// appearance, its tint or its own appearance say, and a control takes the
// app's: a theme other than the system's drew white text on white glass or
// dark on dark (stands in runs 34863293838 and 34864709919). The controls on
// glass are drawn in the system's mode instead, which those stands read in
// both directions.

func everyContentIs(t *testing.T, when string, got []string, want string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: no capsules drawn", when)
	}
	for i, name := range got {
		if name != want {
			t.Errorf("%s: capsule %d's content in %q, want %q (all: %v)", when, i, name, want, got)
		}
	}
}

func TestCapsulesOnGlassDrawTheirControlsInTheSystemsModeFromTheFirstDraw(t *testing.T) {
	if !themeResult.glass {
		t.Skip("NSGlassEffectView is not on this system: the capsules are not on glass")
	}
	everyContentIs(t, "drawn in a dark system", themeResult.firstDrawInDarkSystem, "NSAppearanceNameDarkAqua")
}

func TestTheSystemsModeChangingRedrawsTheCapsulesControlsInIt(t *testing.T) {
	if !themeResult.glass {
		t.Skip("NSGlassEffectView is not on this system: the capsules are not on glass")
	}
	everyContentIs(t, "after the system turned light", themeResult.afterSystemTurnedLight, "NSAppearanceNameAqua")
}

// The tests hear of the system's mode in the process's own centre; the window
// listens where the system says it, and for the name it says.
func TestTheCapsulesListenWhereTheSystemSaysItsModeChanged(t *testing.T) {
	if !themeResult.productCenterDistributed {
		t.Error("outside the tests the capsules listen in a centre other than the distributed one")
	}
	if themeResult.subscribedName != "AppleInterfaceThemeChangedNotification" {
		t.Errorf("the capsules listen for %q", themeResult.subscribedName)
	}
}

func TestTheAppsThemeDoesNotDecideTheCapsulesControlsOnGlass(t *testing.T) {
	if !themeResult.glass {
		t.Skip("NSGlassEffectView is not on this system: the capsules are not on glass")
	}
	everyContentIs(t, "the app dark in a light system", themeResult.afterAppTurnedDark, "NSAppearanceNameAqua")
}

// Vibrancy and the opaque capsules take the app's mode, as the panels do.
func TestCapsulesOffGlassKeepTheAppsMode(t *testing.T) {
	everyContentIs(t, "in vibrancy", themeResult.inVibrancy, "")
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
