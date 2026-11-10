//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// capsuleModel is what the board hands the window for the bar across its top
// (web/js/capsules.js): the page's own words and colours, version 1. Left to
// right the bar has the brand, the fleet, the limits and an update when there
// is one; at its right the counters and the theme (the redesign, T-226).
type capsuleModel struct {
	Version     int            `json:"version"`
	Brand       capsuleBrand   `json:"brand"`
	Fleet       capsuleFleet   `json:"fleet"`
	LimitsLabel string         `json:"limitsLabel"`
	Limits      []capsuleCap   `json:"limits"`
	Update      *capsuleUpdate `json:"update,omitempty"`
	Counters    []capsulePill  `json:"counters"`
	Theme       capsuleTheme   `json:"theme"`
	// Accent is the page's accent, #rrggbb: the fleet's dot and the update.
	Accent string `json:"accent,omitempty"`
	// Opaque is the operator's "no transparency" glass setting: the window
	// goes opaque as it does under Reduce Transparency.
	Opaque bool `json:"opaque,omitempty"`
}

type capsuleBrand struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// capsuleFleet is the fleet the board shows and the menu under its capsule:
// every fleet, then All and Add.
type capsuleFleet struct {
	Name   string              `json:"name"`
	Label  string              `json:"label"`
	Fleets []capsuleFleetEntry `json:"fleets"`
	All    string              `json:"all"`
	Add    string              `json:"add"`
}

type capsuleFleetEntry struct {
	Name    string `json:"name"`
	Waiting int    `json:"waiting,omitempty"`
	Current bool   `json:"current,omitempty"`
}

type capsuleCap struct {
	Label string `json:"label"`
	Text  string `json:"text"`
	Level string `json:"level"`
	Color string `json:"color"`
}

type capsuleUpdate struct {
	Text   string `json:"text"`
	Action string `json:"action"`
}

// capsulePill is a counter: its text in its colour on a tint of it, the dot a
// ring when hollow.
type capsulePill struct {
	Text   string `json:"text"`
	Color  string `json:"color"`
	Hollow bool   `json:"hollow,omitempty"`
}

// capsuleTheme is the theme's three segments: what each is called and the one
// chosen; Label names the group.
type capsuleTheme struct {
	Label  string `json:"label"`
	Choice string `json:"choice"`
	Light  string `json:"light"`
	System string `json:"system"`
	Dark   string `json:"dark"`
}

// segment is the chosen segment of light, system and dark (capsules_darwin.c,
// themeChoices): the system's for anything but light or dark.
func (t capsuleTheme) segment() int {
	switch t.Choice {
	case "light":
		return 0
	case "dark":
		return 2
	}
	return 1
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

// limitValue is a limit's number: "37%" is 37, and the dash of a limit with no
// number is 0.
func limitValue(text string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64)
	if err != nil {
		return 0
	}
	return v
}

// busiestLimit is what the limits capsule says of the limits: the busiest
// one's Name and Text in its Color, and every limit in Tooltip.
type busiestLimit struct {
	Name, Text, Color, Tooltip string
}

// limitSeverity orders the page's levels (web/js/header.js) from the least
// telling to the worst: a limit with no number, an old number, then by use.
var limitSeverity = map[string]int{"off": 1, "stale": 2, "cool": 3, "warm": 4, "hot": 5}

func (m capsuleModel) busiestLimit() busiestLimit {
	if len(m.Limits) == 0 {
		return busiestLimit{}
	}
	var lines []string
	worst := m.Limits[0]
	for _, limit := range m.Limits {
		lines = append(lines, limit.Label+": "+limit.Text)
		s, w := limitSeverity[limit.Level], limitSeverity[worst.Level]
		if s > w || s == w && limitValue(limit.Text) > limitValue(worst.Text) {
			worst = limit
		}
	}
	return busiestLimit{Name: worst.Label, Text: worst.Text, Color: worst.Color, Tooltip: strings.Join(lines, "\n")}
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
