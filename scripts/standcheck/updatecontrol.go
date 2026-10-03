package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The update control beside the brand, held by a stand in one state of Check
// for Updates… (FLEETDECK_STAND_OPEN check-*, web/js/main.js). The orchestrator
// surface reports what it shows as it paints it.
const updateControlPrefix = "fleetdeck-window: the orchestrator surface reports its update control: "

type updateControlReport struct {
	Text    string `json:"text"`
	Button  bool   `json:"button"`
	Problem bool   `json:"problem"`
	// Shown: the panel is on screen, not hidden for want of a terminal.
	Shown bool `json:"shown"`
	// Box is the panel's, in the surface's coordinates; TermTop the top of
	// the terminal it lies over; PageWidth the surface's width.
	Box *struct {
		Top    float64 `json:"top"`
		Left   float64 `json:"left"`
		Right  float64 `json:"right"`
		Bottom float64 `json:"bottom"`
	} `json:"box"`
	TermTop   float64 `json:"termTop"`
	PageWidth float64 `json:"pageWidth"`
	// Clipped: the words are wider or taller than their box, or run past the
	// panel's edge.
	Clipped bool `json:"clipped"`
}

// geometrySlack is how far a box may stand past a line before it counts as
// crossing it: the page measures in fractions of a point.
const geometrySlack = 0.5

// updateControlWants is what each state's frame has to show: the particulars
// the page's stand fixture carries (web/js/main.js, STAND_UPDATE), whether the
// Update button is there, and whether the words are marked as a problem.
var updateControlWants = map[string]struct {
	carries string
	button  bool
	problem bool
}{
	"check-checking":  {"", false, false},
	"check-latest":    {"v1.0.0", false, false},
	"check-failed":    {"no such host", false, true},
	"check-available": {"v1.1.0", true, false},
}

// updateControlCheck is what is wrong with the update control the log's
// orchestrator surface last reported, held to the state the stand opened.
func updateControlCheck(log string, open []string) []string {
	var problems []string
	for _, name := range open {
		want, ok := updateControlWants[name]
		if !ok {
			continue
		}
		last := ""
		for _, line := range strings.Split(log, "\n") {
			if i := strings.Index(line, updateControlPrefix); i >= 0 {
				last = line[i+len(updateControlPrefix):]
			}
		}
		if last == "" {
			problems = append(problems, fmt.Sprintf("the orchestrator did not report its update control, which %s shows", name))
			continue
		}
		var r updateControlReport
		if err := json.Unmarshal([]byte(last), &r); err != nil {
			problems = append(problems, fmt.Sprintf("an update control report from the orchestrator that is not one: %v", err))
			continue
		}
		say := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("the update control on %s ", name)+fmt.Sprintf(format, args...))
		}
		if strings.TrimSpace(r.Text) == "" {
			say("shows no words")
		} else if want.carries != "" && !strings.Contains(r.Text, want.carries) {
			say("says %q, without %s", r.Text, want.carries)
		}
		if r.Button != want.button {
			say("has a button: %v, want %v", r.Button, want.button)
		}
		if r.Problem != want.problem {
			say("is marked as a problem: %v, want %v", r.Problem, want.problem)
		}
		// Where it lies. The panel lies over the top of the terminal and no
		// higher -- the brand row, the window's buttons and the island's head
		// above it stay uncovered -- inside the surface, with its words whole.
		if !r.Shown {
			say("is not shown")
		}
		if r.Clipped {
			say("has its words clipped: they do not fit inside its box")
		}
		if r.Box == nil {
			say("reports no box")
			continue
		}
		if r.Box.Top < r.TermTop-geometrySlack {
			say("reaches above the terminal: its top is at %v, the terminal's at %v", r.Box.Top, r.TermTop)
		}
		if r.Box.Left < -geometrySlack || r.Box.Right > r.PageWidth+geometrySlack {
			say("runs past the page: %v to %v in %v", r.Box.Left, r.Box.Right, r.PageWidth)
		}
	}
	return problems
}
