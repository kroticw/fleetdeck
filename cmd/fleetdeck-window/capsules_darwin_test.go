package main

// The bar is drawn once, on the main thread, in TestMain (menu_darwin_test.go),
// on a window never put on screen; the tests below only read what was found.

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
)

// barModel is the bar of the redesign (T-226) as a board in Russian hands it
// to the window: two fleets, two limits, an update and both counters.
const barModel = `{"version":1,
	"brand":{"name":"fleetdeck","version":"1.8.0"},
	"fleet":{"name":"obsidian","label":"Флот","fleets":[{"name":"obsidian","waiting":1,"current":true},{"name":"work"}],"all":"Все флоты","add":"Завести флот…"},
	"limitsLabel":"Лимиты",
	"limits":[{"label":"Claude","text":"42%","level":"cool","color":"#2f9e44"},{"label":"Codex","text":"63%","level":"warm","color":"#d9480f"}],
	"update":{"text":"Доступна 1.9.0","action":"Обновить"},
	"counters":[{"text":"1 ждёт ответа","color":"#c2410c"},{"text":"0 остановились","color":"#6b7280","hollow":true}],
	"theme":{"label":"Тема","choice":"dark","light":"Светлая","system":"Как в системе","dark":"Тёмная"},
	"accent":"#4f56d9"}`

var (
	capsulesResult capsulesProbe
	themeResult    capsuleThemeProbe
	// plainCount is how many parts a bar with no update and no counters shows.
	plainCount int
)

func collectCapsuleResults() {
	m, err := parseCapsuleModel(json.RawMessage(barModel))
	if err != nil {
		panic(err)
	}
	capsulesResult = probeCapsulesForTest(m)
	collectCapsuleLayoutResults(m)
	themeResult = probeCapsuleThemeForTest(m)
	m.Update, m.Counters = nil, nil
	plainCount = probeCapsuleCountForTest(m)
}

// --- the bar's parts ------------------------------------------------------------

func TestTheBarDrawsThePagesModel(t *testing.T) {
	r := capsulesResult
	want := map[string]string{
		"brandName": "fleetdeck", "brandVersion": "1.8.0",
		"fleetName": "obsidian", "fleetLabel": "Флот",
		"limitsLabel": "Лимиты", "busyName": "Codex", "busyText": "63%", "limitsTooltip": "Claude: 42%\nCodex: 63%",
		"updateText": "Доступна 1.9.0", "updateAction": "Обновить",
		"counter0": "1 ждёт ответа", "counter1": "0 остановились",
	}
	if !reflect.DeepEqual(r.texts, want) {
		t.Fatalf("texts = %v\nwant    %v", r.texts, want)
	}
	if got := []string{"brand", "fleet", "limits", "update", "counter", "counter", "theme"}; !reflect.DeepEqual(r.slots, got) {
		t.Fatalf("parts = %v, want %v", r.slots, got)
	}
	if r.capsuleCount != 7 {
		t.Fatalf("parts shown = %d, want all 7", r.capsuleCount)
	}
	if !r.brandIcon {
		t.Error("the brand shows no app icon")
	}
}

func TestABarWithNoUpdateAndNoCountersShowsTheRest(t *testing.T) {
	if plainCount != 4 {
		t.Fatalf("parts shown = %d, want the brand, the fleet, the limits and the theme", plainCount)
	}
}

// The colours are the page's: the fleet's dot is the accent, each limit a dot
// in its level's colour -- a level indicator drew nothing on glass -- and the
// busiest limit's number in its colour. A counter is its colour on a tint of it.
func TestTheBarsMarksAreInThePagesColours(t *testing.T) {
	g := capsulesResult.greens
	for what, hex := range map[string]uint8{"fleetDot": 0x56, "dot0": 0x9e, "dot1": 0x48, "busyText": 0x48, "counter0": 0x41, "counterTint0": 0x41} {
		if math.Round(g[what]*255) != float64(hex) {
			t.Errorf("%s: green %.3f, want %#x", what, g[what], hex)
		}
	}
	if !reflect.DeepEqual(capsulesResult.counterHollow, []bool{false, true}) {
		t.Errorf("hollow dots = %v, want only the stalled counter's", capsulesResult.counterHollow)
	}
}

// Every part is centred on the bar's line, the line of the window's buttons.
func TestEveryPartIsCentredOnTheBarsLine(t *testing.T) {
	r := capsulesResult
	for i, name := range r.slots {
		if mid := r.slotY[i] + r.slotH[i]/2; math.Abs(mid-capsuleHeight/2) > 0.5 {
			t.Errorf("%s: centred %v into the row, want %v", name, mid, capsuleHeight/2)
		}
	}
}

func TestTheThemeIsThreeSegmentsWithTheChoiceSelected(t *testing.T) {
	r := capsulesResult
	if r.themeSegments != 3 || r.themeSelected != 2 {
		t.Fatalf("segments %d, selected %d; want 3 with the dark one", r.themeSegments, r.themeSelected)
	}
	if r.themeTooltips != [3]string{"Светлая", "Как в системе", "Тёмная"} {
		t.Fatalf("tooltips = %q", r.themeTooltips)
	}
	if r.themeImages[0] != r.themeImages[1] || r.themeImages[1] != r.themeImages[2] {
		t.Fatalf("images = %v: all symbols or all marks", r.themeImages)
	}
}

// v0.10.1 on the operator's macOS: the selected tab was a rounded rectangle in
// its round capsule. From macOS 26 a segmented control has a border shape, and
// left automatic it draws a rounded rectangle at this size. Asked for a capsule,
// its selected segment is one too.
// capsuleOverRoundedInset is how much further in a capsule's top row starts than
// a rounded rectangle's, at the least: 0.46 against 0.19 on a 2x screen.
const capsuleOverRoundedInset = 0.05

func TestTheSelectedThemeIsACapsuleInsideItsCapsule(t *testing.T) {
	r := capsulesResult
	if r.borderShape < 0 {
		t.Skip("NSControl.borderShape is not on this system (before macOS 26)")
	}
	if r.borderShape != 1 {
		t.Errorf("the theme's border shape is %d, want 1 (NSControlBorderShapeCapsule)", r.borderShape)
	}
	if r.roundedTopInset <= 0 || r.selectedTopInset < r.roundedTopInset+capsuleOverRoundedInset {
		t.Errorf("the selected segment's fill starts %.2f of its height in at its top, a rounded rectangle's drawn in its place %.2f: want a capsule's, at least %.2f further in", r.selectedTopInset, r.roundedTopInset, capsuleOverRoundedInset)
	}
}

// In 1.8.0 the page's fleet menu opened to the right of its button. The bar's
// opens under the capsule, its left edge on the capsule's.
func TestTheFleetsMenuOpensUnderItsCapsule(t *testing.T) {
	r := capsulesResult
	if math.Abs(r.menuX-r.fleetX) > 0.5 {
		t.Errorf("the menu's left edge at %v, the capsule's at %v", r.menuX, r.fleetX)
	}
	if r.menuY < r.fleetBottom || r.menuY > r.fleetBottom+10 {
		t.Errorf("the menu's top at %v, want just under the capsule's bottom at %v", r.menuY, r.fleetBottom)
	}
}

func TestTheFleetsMenuListsEveryFleetThenAllAndAdd(t *testing.T) {
	m := capsulesResult.menu
	if len(m) != 5 {
		t.Fatalf("menu = %+v, want two fleets, a separator, All and Add", m)
	}
	if m[0].title != "obsidian" || !m[0].checked || m[0].waiting != 1 {
		t.Errorf("first = %+v, want obsidian checked with one waiting", m[0])
	}
	if m[1].title != "work" || m[1].checked || m[1].waiting != 0 {
		t.Errorf("second = %+v, want work, unchecked, none waiting", m[1])
	}
	if !m[2].separator || m[3].title != "Все флоты" || m[4].title != "Завести флот…" {
		t.Errorf("tail = %+v", m[2:])
	}
}

func TestAPressInTheBarBecomesTheControllersAction(t *testing.T) {
	want := []string{"fleet:work", "fleets", "newFleet", "limits", "update", "theme:dark"}
	if !reflect.DeepEqual(capsulesResult.presses, want) {
		t.Fatalf("presses = %v, want %v", capsulesResult.presses, want)
	}
}

func TestADrawnAgainRowReplacesTheCapsulesInsteadOfAddingThem(t *testing.T) {
	if capsulesResult.countAfterRedraw != 7 {
		t.Fatalf("after a second draw: %d, want the same seven parts in one row", capsulesResult.countAfterRedraw)
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
	for _, what := range []string{"fleet", "limits", "update", "theme"} {
		if !capsulesResult.insideGlass[what] {
			t.Errorf("%s, drawn on glass, is not its glass's content view: the glass draws over it", what)
		}
	}
}

// A click at the middle of each capsule reaches its control, on a window laid
// out as one on screen is: before layout a glass's content view has no size,
// and a hit test stops at the glass. The fleet and the limits are one control
// from anywhere on them: a chevron alone was too small to hit.
func TestAClickOnEachCapsuleOnGlassReachesItsControl(t *testing.T) {
	if !frameResult.glassAvailable {
		t.Skip("NSGlassEffectView is not on this system: the capsules are not on glass")
	}
	for _, what := range []string{"fleet", "limits", "update", "theme"} {
		if !capsulesResult.clicksReach[what] {
			t.Errorf("a click at the middle of %s, drawn on glass, does not reach it", what)
		}
	}
}

func TestAClickInTheGapBetweenTheGroupsReachesTheBand(t *testing.T) {
	if !capsulesResult.rowPassesThrough {
		t.Fatal("the capsule row's container takes a click between its groups, over the band")
	}
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
			t.Errorf("part %d's content has an appearance of its own, %q, instead of the app's", i, name)
		}
	}
}

// The glass capsules only: the brand has no capsule and a counter's tint is the
// page's colour.
func TestOpaqueCapsulesTakeTheirBackgroundFromTheAppsTheme(t *testing.T) {
	glassy := map[string]bool{"fleet": true, "limits": true, "update": true, "theme": true}
	for _, c := range []struct {
		theme  string
		got    []float64
		darker bool
	}{{"dark", themeResult.opaqueInDarkApp, true}, {"light", themeResult.opaqueInLightApp, false}} {
		if len(c.got) != len(capsulesResult.slots) {
			t.Fatalf("app %s: %d parts measured, want %d", c.theme, len(c.got), len(capsulesResult.slots))
		}
		for i, b := range c.got {
			if name := capsulesResult.slots[i]; glassy[name] && (b < 0 || (b < 0.5) != c.darker) {
				t.Errorf("app %s: %s's background is %.2f light, want it %s", c.theme, name, b, map[bool]string{true: "dark", false: "light"}[c.darker])
			}
		}
	}
}

func TestTheThemeProbeLeavesTheAppsAppearanceAsItFoundIt(t *testing.T) {
	if themeResult.appAppearanceAt != themeResult.appAppearanceBefore {
		t.Fatalf("app appearance %q after the probe, %q before", themeResult.appAppearanceAt, themeResult.appAppearanceBefore)
	}
}

// --- a row in a narrow window ----------------------------------------------------

// v0.10.1's first stand, 1000 by 700 with both panels unfolded: the board's
// layout gave the frame its geometry after the surfaces were created, and so
// after the row was drawn. Read after the layout, as the stand's screenshot is,
// and again after a resize, which would have hidden it. A 1000 pt window leaves
// the board's island its minimum only with both panels narrowed.
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
			t.Errorf("%s: panels %v and %v, want both narrowed for the board", s.when, s.f.orchestrator, s.f.sessions)
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

// v0.10.1's stands, all six alike (run 34930674108): the board kept the insets
// of panels at their widths and ended short of the sessions glass. After every
// stage the last insets the board heard are those of the native panels as laid
// out.
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
	layoutResults []capsuleLayoutProbe
	// resizedResult: drawn at 1440, then the window made 1000 with both panels
	// unfolded, the capsules not drawn again -- how a window is resized.
	resizedResult capsuleLayoutProbe
)

func collectCapsuleLayoutResults(m capsuleModel) {
	for _, width := range []float64{1728, 1000, 800} {
		for _, folded := range []bool{false, true} {
			layoutResults = append(layoutResults, probeCapsuleLayoutForTest(m, width, 0, folded))
		}
	}
	resizedResult = probeCapsuleLayoutForTest(m, 1000, 1728, false)
	standResult = probeCapsuleStandForTest(m)
	foldResults = probeFoldsForTest(m)
	regrowResults = []capsuleRegrowProbe{probeCapsuleRegrowForTest(m, 1000, 1728), probeCapsuleRegrowForTest(m, 1728, 1000)}
}

// regrowResults: the row drawn at one width and laid out at a much wider or
// narrower one without a redraw, as entering and leaving full screen do.
var regrowResults []capsuleRegrowProbe

// foldResults: a stand's window in every fold of its panels (probeFoldsForTest).
var foldResults []foldedStandFrame

// v0.11.0's dev build on macOS 27 (the operator's frame 1374): the Board/Docs
// tabs lay under the window's buttons. In every fold no part shown lies under
// the close, minimize or zoom button.
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
// autoresizing moved by that width a second time.
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
		t.Fatalf("%v: %d parts named %s in %+v", p, len(found), name, p.capsules)
	}
	return found[0]
}

// countersShown is whether the row shows its counters: all of them or none.
func (p capsuleLayoutProbe) countersShown(t *testing.T) bool {
	t.Helper()
	counters := p.named("counter")
	if len(counters) == 0 {
		t.Fatalf("%v: no counters drawn", p)
	}
	for _, c := range counters[1:] {
		if c.visible != counters[0].visible {
			t.Fatalf("%v: some counters shown and some not: %+v", p, counters)
		}
	}
	return counters[0].visible
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
		t.Errorf("%v: window minimum %v, want the 800 pt window below it", p, p.minContentWidth)
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

// Only the counters give way: the brand, the fleet, the limits, the update and
// the theme are shown whole at every width.
func TestEveryPartButTheCountersIsAlwaysWhole(t *testing.T) {
	wide := layoutResults[0]
	for _, p := range allLayouts() {
		for _, name := range []string{"brand", "fleet", "limits", "update", "theme"} {
			c, want := p.one(t, name), wide.one(t, name)
			if !c.visible || c.w != want.w {
				t.Errorf("%v: %s visible %v, %v wide; want shown at its %v", p, name, c.visible, c.w, want.w)
			}
		}
	}
}

// A wide window shows the counters, a narrow one hides them, and a narrower
// window never shows them where a wider one did not.
func TestANarrowWindowHidesTheCountersFirst(t *testing.T) {
	if !layoutResults[0].countersShown(t) {
		t.Errorf("%v: counters hidden with room for them", layoutResults[0])
	}
	for _, folded := range []bool{false, true} {
		shown := true
		for _, p := range layoutResults {
			if p.folded != folded {
				continue
			}
			s := p.countersShown(t)
			if s && !shown {
				t.Errorf("%v: counters shown where a wider window hid them", p)
			}
			shown = s
		}
	}
	if layoutResults[4].countersShown(t) {
		t.Errorf("%v: counters shown in the narrowest window", layoutResults[4])
	}
}

func TestAWindowResizedWithoutARedrawTakesTheFormOfItsWidth(t *testing.T) {
	if got, want := resizedResult.countersShown(t), layoutResults[2].countersShown(t); got != want {
		t.Fatalf("%v: counters shown %v, want %v as drawn at that width", resizedResult, got, want)
	}
}

// The window's minimum content width is the wider of the frame -- both panels
// at their readable width, the board's island and the margins -- and the bar's
// row in its narrowest form from where it starts to the right margin: the left
// group and the theme, with the row's 8 pt gaps between them.
func TestTheWindowsMinimumWidthIsTheFrameAndTheNarrowestRow(t *testing.T) {
	for _, p := range allLayouts() {
		parts := p.one(t, "brand").w + 8 + p.one(t, "fleet").w + 8 + p.one(t, "limits").w + 8 + p.one(t, "update").w + 8 + p.one(t, "theme").w
		if math.Abs(p.rowMin-parts) > 1e-9 {
			t.Errorf("%v: row minimum %v, want %v", p, p.rowMin, parts)
		}
		if want := math.Max(frameMinWidth(), p.geometry.Capsules.X+parts+panelMargin); math.Abs(p.minContentWidth-want) > 1e-9 {
			t.Errorf("%v: window minimum %v, want %v", p, p.minContentWidth, want)
		}
		if p.minAfterClear != 0 {
			t.Errorf("%v: minimum after the row is cleared = %v, want none", p, p.minAfterClear)
		}
	}
}
