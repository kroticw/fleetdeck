//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// capsuleModel is what the board hands the window for its capsule row
// (web/js/capsules.js): the page's own words and colours, version 1.
type capsuleModel struct {
	Version int          `json:"version"`
	Tabs    []capsuleTab `json:"tabs"`
	NewCard capsuleLabel `json:"newCard"`
	Theme   capsuleLabel `json:"theme"`
	Limits  []capsuleCap `json:"limits"`
}

type capsuleTab struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Selected bool   `json:"selected"`
}

type capsuleLabel struct {
	Label string `json:"label"`
}

type capsuleCap struct {
	Label string `json:"label"`
	Text  string `json:"text"`
	Level string `json:"level"`
	Color string `json:"color"`
}

func parseCapsuleModel(raw json.RawMessage) (capsuleModel, error) {
	var m capsuleModel
	if err := json.Unmarshal(raw, &m); err != nil {
		return capsuleModel{}, fmt.Errorf("capsule model: %w", err)
	}
	if m.Version != hostVersion {
		return capsuleModel{}, fmt.Errorf("capsule model: version %d, this window draws %d", m.Version, hostVersion)
	}
	return m, nil
}

// limitValue is a limit's text as the level indicator's value: "37%" is 37,
// and the dash of a limit with no number is an empty indicator.
func limitValue(text string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64)
	if err != nil {
		return 0
	}
	return v
}

// compactLimits is every limit as one capsule of text, for a capsule row with
// no room for their indicators: Text in the row, Tooltip on it, and Color the
// worst limit's.
type compactLimits struct {
	Text, Tooltip, Color string
}

// limitSeverity orders the page's levels (web/js/header.js) from the least
// telling to the worst: a limit with no number, an old number, then by use.
var limitSeverity = map[string]int{"off": 1, "stale": 2, "cool": 3, "warm": 4, "hot": 5}

func (m capsuleModel) compactLimits() compactLimits {
	if len(m.Limits) == 0 {
		return compactLimits{}
	}
	var parts, lines []string
	worst := m.Limits[0]
	for _, limit := range m.Limits {
		parts = append(parts, limit.Label+" "+limit.Text)
		lines = append(lines, limit.Label+": "+limit.Text)
		if limitSeverity[limit.Level] > limitSeverity[worst.Level] {
			worst = limit
		}
	}
	return compactLimits{Text: strings.Join(parts, " · "), Tooltip: strings.Join(lines, "\n"), Color: worst.Color}
}

// hexColor reads #rrggbb, the form the page's colour tokens take (web/app.css).
func hexColor(s string) (r, g, b uint8, ok bool) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v), true
}
