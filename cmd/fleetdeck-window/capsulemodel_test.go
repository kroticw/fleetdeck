//go:build darwin

package main

import (
	"encoding/json"
	"testing"
)

func TestACapsuleModelOfAnotherVersionIsRefused(t *testing.T) {
	if _, err := parseCapsuleModel(json.RawMessage(`{"version":2}`)); err == nil {
		t.Fatal("a model of another version must not be drawn")
	}
}

func TestACapsuleModelThatIsNotJSONIsRefused(t *testing.T) {
	if _, err := parseCapsuleModel(json.RawMessage(`{"version":`)); err == nil {
		t.Fatal("a broken model must not be drawn")
	}
}

// The bar of the redesign (T-226), in the page's words and colours.
func TestACapsuleModelKeepsThePagesWordsAndColours(t *testing.T) {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,
		"brand":{"name":"fleetdeck","version":"1.8.0"},
		"fleet":{"name":"obsidian","label":"Флот","fleets":[{"name":"obsidian","waiting":1,"current":true},{"name":"work"}],"all":"Все флоты","add":"Завести флот…"},
		"limitsLabel":"Лимиты",
		"limits":[{"label":"Codex","text":"63%","level":"warm","color":"#d9480f"}],
		"update":{"text":"Доступна 1.9.0","action":"Обновить"},
		"counters":[{"text":"1 ждёт ответа","color":"#c2410c"},{"text":"0 остановились","color":"#6b7280","hollow":true}],
		"theme":{"label":"Тема","choice":"dark","light":"Светлая","system":"Как в системе","dark":"Тёмная"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Brand != (capsuleBrand{Name: "fleetdeck", Version: "1.8.0"}) {
		t.Fatalf("brand = %+v", m.Brand)
	}
	if m.Fleet.Name != "obsidian" || m.Fleet.All != "Все флоты" || m.Fleet.Add != "Завести флот…" || len(m.Fleet.Fleets) != 2 ||
		m.Fleet.Fleets[0] != (capsuleFleetEntry{Name: "obsidian", Waiting: 1, Current: true}) {
		t.Fatalf("fleet = %+v", m.Fleet)
	}
	if m.LimitsLabel != "Лимиты" || len(m.Limits) != 1 || m.Limits[0].Color != "#d9480f" {
		t.Fatalf("limits = %q %+v", m.LimitsLabel, m.Limits)
	}
	if m.Update == nil || m.Update.Text != "Доступна 1.9.0" || m.Update.Action != "Обновить" {
		t.Fatalf("update = %+v", m.Update)
	}
	if len(m.Counters) != 2 || m.Counters[0].Hollow || !m.Counters[1].Hollow || m.Counters[1].Text != "0 остановились" {
		t.Fatalf("counters = %+v", m.Counters)
	}
	if m.Theme.Choice != "dark" || m.Theme.System != "Как в системе" {
		t.Fatalf("theme = %+v", m.Theme)
	}
}

// The limits capsule's system menu (T-226): every model and its windows.
func TestACapsuleModelCarriesTheLimitsMenu(t *testing.T) {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,"limitsMenu":{"title":"Лимиты","note":"сначала самые занятые",
		"models":[{"name":"Codex","kind":"5 ч и неделя","color":"#a15c00","rows":[{"title":"5 часов","text":"63%","pct":63,"note":"сброс через 2 ч","color":"#a15c00"},{"title":"7 дней","text":"—","pct":-1,"note":"","color":"#8791a0"}]}],
		"updated":"обновлено Codex: 1 мин","sources":"Источники…"}}`))
	if err != nil {
		t.Fatal(err)
	}
	menu := m.LimitsMenu
	if menu == nil || menu.Title != "Лимиты" || menu.Sources != "Источники…" || menu.Updated != "обновлено Codex: 1 мин" || len(menu.Models) != 1 {
		t.Fatalf("menu = %+v", menu)
	}
	rows := menu.Models[0].Rows
	if len(rows) != 2 || rows[0] != (capsuleLimitsRow{Title: "5 часов", Text: "63%", Pct: 63, Note: "сброс через 2 ч", Color: "#a15c00"}) || rows[1].Pct != -1 {
		t.Fatalf("rows = %+v", rows)
	}
	if plain, _ := parseCapsuleModel(json.RawMessage(`{"version":1}`)); plain.LimitsMenu != nil {
		t.Fatal("no menu in the page's model is no menu")
	}
}

func TestNoUpdateIsNoUpdateCapsule(t *testing.T) {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Update != nil {
		t.Fatalf("update = %+v, want none", m.Update)
	}
}

// The page's ui.theme is "", light or dark; the window's three segments are
// light, system and dark, and anything else is the system's.
func TestTheThemeChoiceIsOneOfTheThreeSegments(t *testing.T) {
	for choice, want := range map[string]int{"light": 0, "system": 1, "": 1, "sepia": 1, "dark": 2} {
		if got := (capsuleTheme{Choice: choice}).segment(); got != want {
			t.Errorf("segment of %q = %d, want %d", choice, got, want)
		}
	}
}

func TestAColourIsReadFromThePagesHex(t *testing.T) {
	r, g, b, ok := hexColor("#2f9e44")
	if !ok || r != 0x2f || g != 0x9e || b != 0x44 {
		t.Fatalf("hexColor = %v %v %v %v", r, g, b, ok)
	}
	if _, _, _, ok := hexColor("rgb(1, 2, 3)"); ok {
		t.Fatal("only #rrggbb is read; the page's tokens are hex")
	}
}

// The limits capsule names the busiest limit, in its colour, and says every
// limit to a pointer and to VoiceOver.
func TestTheBusiestLimitIsTheWorstAndTheTooltipEveryLimit(t *testing.T) {
	m := capsuleModel{Limits: []capsuleCap{
		{Label: "Claude", Text: "42%", Level: "cool", Color: "#2f9e44"},
		{Label: "Codex", Text: "91%", Level: "hot", Color: "#e03131"},
	}}
	b := m.busiestLimit()
	if b.Name != "Codex" || b.Text != "91%" || b.Color != "#e03131" {
		t.Fatalf("busiest = %+v", b)
	}
	if b.Tooltip != "Claude: 42%\nCodex: 91%" {
		t.Fatalf("tooltip = %q", b.Tooltip)
	}
}

func TestTheWorstLimitIsHotThenWarmThenCoolThenStaleThenOff(t *testing.T) {
	order := []string{"hot", "warm", "cool", "stale", "off"}
	for i := 0; i+1 < len(order); i++ {
		worse, better := order[i], order[i+1]
		for _, limits := range [][]capsuleCap{
			{{Level: better, Color: "#000002"}, {Level: worse, Color: "#000001"}},
			{{Level: worse, Color: "#000001"}, {Level: better, Color: "#000002"}},
		} {
			if got := (capsuleModel{Limits: limits}).busiestLimit().Color; got != "#000001" {
				t.Errorf("%v: colour %q, want %s's", limits, got, worse)
			}
		}
	}
}

// Two limits at the same level: the one used more is the busier.
func TestOfTwoLimitsAtOneLevelTheHigherNumberIsTheBusier(t *testing.T) {
	m := capsuleModel{Limits: []capsuleCap{
		{Label: "Claude", Text: "51%", Level: "warm"},
		{Label: "Codex", Text: "63%", Level: "warm"},
		{Label: "Gemini", Text: "—", Level: "warm"},
	}}
	if b := m.busiestLimit(); b.Name != "Codex" {
		t.Fatalf("busiest = %+v, want Codex", b)
	}
}

func TestNoLimitsAreNoBusiestLimit(t *testing.T) {
	if b := (capsuleModel{}).busiestLimit(); b != (busiestLimit{}) {
		t.Fatalf("busiest = %+v, want nothing", b)
	}
}

// The page's accent and its glass setting ride on the model: the window tints
// the fleet's dot and the update with the one and goes opaque on the other.
func TestACapsuleModelCarriesTheAccentAndTheOpaqueWish(t *testing.T) {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,"accent":"#4f56d9","opaque":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Accent != "#4f56d9" || !m.Opaque {
		t.Fatalf("model = %+v", m)
	}
}
