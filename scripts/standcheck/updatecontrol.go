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
}

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
	}
	return problems
}
