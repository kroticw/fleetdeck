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
