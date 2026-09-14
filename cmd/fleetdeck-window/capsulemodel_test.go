//go:build darwin

package main

import (
	"encoding/json"
	"testing"
)

func TestACapsuleModelOfAnotherVersionIsRefused(t *testing.T) {
	if _, err := parseCapsuleModel(json.RawMessage(`{"version":2,"tabs":[]}`)); err == nil {
		t.Fatal("a model of another version must not be drawn")
	}
}

func TestACapsuleModelThatIsNotJSONIsRefused(t *testing.T) {
	if _, err := parseCapsuleModel(json.RawMessage(`{"version":`)); err == nil {
		t.Fatal("a broken model must not be drawn")
	}
}

func TestACapsuleModelKeepsThePagesWordsAndColours(t *testing.T) {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,"tabs":[{"id":"board","label":"Доска","selected":true},{"id":"docs","label":"Доки","selected":false}],"newCard":{"label":"+ карточка"},"theme":{"label":"тема: авто"},"limits":[{"label":"5ч","text":"37%","level":"cool","color":"#2f9e44"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Tabs[0].Label != "Доска" || !m.Tabs[0].Selected || m.Tabs[1].ID != "docs" {
		t.Fatalf("tabs = %+v", m.Tabs)
	}
	if m.NewCard.Label != "+ карточка" || m.Theme.Label != "тема: авто" {
		t.Fatalf("buttons = %+v %+v", m.NewCard, m.Theme)
	}
	if len(m.Limits) != 1 || m.Limits[0].Color != "#2f9e44" || m.Limits[0].Text != "37%" || m.Limits[0].Level != "cool" {
		t.Fatalf("limits = %+v", m.Limits)
	}
}

func TestALimitsPercentageIsReadForTheLevelIndicator(t *testing.T) {
	cases := map[string]float64{"37%": 37, "100%": 100, "—": 0, "": 0}
	for text, want := range cases {
		if got := limitValue(text); got != want {
			t.Fatalf("limitValue(%q) = %v, want %v", text, got, want)
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

// In a narrow window the limits are one capsule of text: every limit's label
// and value in the model's order, coloured as the worst of them.
func TestTheCompactLimitsAreEveryLimitInOneLineColouredAsTheWorst(t *testing.T) {
	m := capsuleModel{Limits: []capsuleCap{
		{Label: "5h", Text: "42%", Level: "cool", Color: "#2f9e44"},
		{Label: "7d", Text: "91%", Level: "hot", Color: "#e03131"},
	}}
	c := m.compactLimits()
	if c.Text != "5h 42% · 7d 91%" {
		t.Fatalf("text = %q", c.Text)
	}
	if c.Tooltip != "5h: 42%\n7d: 91%" {
		t.Fatalf("tooltip = %q", c.Tooltip)
	}
	if c.Color != "#e03131" {
		t.Fatalf("colour = %q, want the hot limit's", c.Color)
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
			if got := (capsuleModel{Limits: limits}).compactLimits().Color; got != "#000001" {
				t.Errorf("%v: colour %q, want %s's", limits, got, worse)
			}
		}
	}
}

func TestNoLimitsAreNoCompactText(t *testing.T) {
	if c := (capsuleModel{}).compactLimits(); c != (compactLimits{}) {
		t.Fatalf("compact = %+v, want nothing", c)
	}
}
