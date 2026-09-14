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
