package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The capsules the pages draw in the window's material (T-070): the
// orchestrator island's head and the new card form, and the panels that float
// over content, the new card form itself and the fleet menu's list. Each surface
// reports them (web/js/standcontrols.js) as it lays out; the last report is what
// the frame shows.
const (
	orchestratorControlsPrefix = "fleetdeck-window: the orchestrator surface reports its controls: "
	boardControlsPrefix        = "fleetdeck-window: the board reports its controls: "
)

// The contrast WCAG asks of text: 4.5:1.
const minContrast = 4.5

// control is one capsule or panel as its page measured it. FillAlpha already
// takes in the opacity of the control and of what it lies in. Whether it floats
// is not read from the page: floatingPanels says which do.
type control struct {
	Name      string  `json:"name"`
	Height    float64 `json:"height"`
	Radius    float64 `json:"radius"`
	FillAlpha float64 `json:"fillAlpha"`
	Backdrop  string  `json:"backdrop"`
	Contrast  float64 `json:"contrast"`
	// PlaceholderContrast is an empty field's placeholder's, nil for a control
	// showing none or one the page could not measure: PlaceholderMeasured is
	// false then, for a WebKit that answers ::placeholder with the field's own
	// style (the CI stand's macOS 26).
	PlaceholderContrast *float64 `json:"placeholderContrast"`
	PlaceholderMeasured *bool    `json:"placeholderMeasured"`
	Disabled            bool     `json:"disabled"`
}

type controlsReport struct {
	Surface  string    `json:"surface"`
	Report   string    `json:"report"`
	Glass    string    `json:"glass"`
	Controls []control `json:"controls"`
}

// floatingPanels are frosted over content in glass; every other control is a
// capsule lying on its surface.
var floatingPanels = map[string]bool{"newCard": true, "fleetList": true}

// islandHead is what the orchestrator island's head always shows unfolded.
var islandHead = []string{"editPencil", "picker", "fleetButton"}

// opened is what each thing a stand opens has to report, by surface.
var opened = map[string]struct {
	surface string
	names   []string
}{
	"newcard":   {"board", []string{"newCard", "newCardTitle", "newCardZone", "newCardCreate", "newCardCancel"}},
	"fleetmenu": {"orchestrator", []string{"fleetList"}},
}

// openList is FLEETDECK_STAND_OPEN as the window reads it: names, comma-separated.
func openList(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

// controlsNotes is what the log's pages could not measure of their capsules: said,
// not failed, since a stand's WebKit decides it, not the page.
func controlsNotes(log string) []string {
	_, reports, _ := readControls(log)
	var notes []string
	for _, surface := range []string{"orchestrator", "board"} {
		for _, c := range reports[surface].Controls {
			if c.PlaceholderMeasured != nil && !*c.PlaceholderMeasured {
				notes = append(notes, fmt.Sprintf("the %s's %s placeholder was not measured: this WebKit answers ::placeholder with the field's own style", surface, c.Name))
			}
		}
	}
	return notes
}

// readControls is the last frame the log measured, the last controls report of
// each surface, and the reports that are not ones.
func readControls(log string) (*frameReport, map[string]controlsReport, []string) {
	var frame *frameReport
	reports := map[string]controlsReport{}
	var problems []string
	for _, line := range strings.Split(log, "\n") {
		if i := strings.Index(line, framePrefix); i >= 0 {
			var f frameReport
			if json.Unmarshal([]byte(line[i+len(framePrefix):]), &f) == nil {
				frame = &f
			}
		}
		for surface, prefix := range map[string]string{"orchestrator": orchestratorControlsPrefix, "board": boardControlsPrefix} {
			i := strings.Index(line, prefix)
			if i < 0 {
				continue
			}
			var r controlsReport
			if err := json.Unmarshal([]byte(line[i+len(prefix):]), &r); err != nil {
				problems = append(problems, fmt.Sprintf("a controls report from the %s that is not one: %v", surface, err))
				continue
			}
			reports[surface] = r
		}
	}
	return frame, reports, problems
}

// controlsCheck is what is wrong with the capsules the log's pages drew; open is
// what the stand opened. Nothing when the log has no frame: check says so.
func controlsCheck(log string, open []string) []string {
	frame, reports, problems := readControls(log)
	if frame == nil {
		return problems
	}
	required := map[string][]string{}
	if !orchestratorFolded(*frame) {
		required["orchestrator"] = append(required["orchestrator"], islandHead...)
	}
	for _, name := range open {
		if o, ok := opened[name]; ok {
			required[o.surface] = append(required[o.surface], o.names...)
		}
	}
	for _, surface := range []string{"orchestrator", "board"} {
		r, ok := reports[surface]
		if !ok {
			problems = append(problems, fmt.Sprintf("no controls report from the %s: its capsules were never measured", surface))
			continue
		}
		if r.Glass != frame.Glass {
			problems = append(problems, fmt.Sprintf("the %s's page draws its capsules for %s, the window's material is %s", surface, r.Glass, frame.Glass))
		}
		reported := map[string]bool{}
		for _, c := range r.Controls {
			reported[c.Name] = true
			problems = append(problems, controlProblems(surface, frame.Glass, c)...)
		}
		for _, name := range required[surface] {
			if !reported[name] {
				problems = append(problems, fmt.Sprintf("the %s did not report %s, which it shows", surface, name))
			}
		}
	}
	return problems
}

func controlProblems(surface, glass string, c control) []string {
	var out []string
	say := func(format string, args ...any) {
		out = append(out, fmt.Sprintf("the %s's %s ", surface, c.Name)+fmt.Sprintf(format, args...))
	}
	solid := glass == "opaque"
	blurred := c.Backdrop != "" && c.Backdrop != "none"
	// A disabled control is dimmed on purpose (web/app.css): its fill is not
	// held to the material, nor its text to the contrast.
	switch {
	case c.Disabled:
	case solid && c.FillAlpha < 1:
		say("is see-through (fill %v) with no glass: it has to be solid", c.FillAlpha)
	case !solid && c.FillAlpha >= 1:
		say("is solid on %s: a capsule of glass has to be see-through", glass)
	}
	if floatingPanels[c.Name] {
		switch {
		case solid && blurred:
			say("blurs what is under it (%s) with no glass", c.Backdrop)
		case !solid && !blurred:
			say("floats over content on %s with no blur under it", glass)
		}
	} else {
		if blurred {
			say("blurs what is under it (%s): glass on glass", c.Backdrop)
		}
		if c.Radius < c.Height/2-edgeSlack {
			say("is not a capsule: radius %v at a height of %v", c.Radius, c.Height)
		}
	}
	if !c.Disabled && c.Contrast < minContrast {
		say("text has a contrast of %v over the worst ground under it, under %v:1", c.Contrast, minContrast)
	}
	if !c.Disabled && c.PlaceholderContrast != nil && *c.PlaceholderContrast < minContrast {
		say("placeholder has a contrast of %v over the worst ground under it, under %v:1", *c.PlaceholderContrast, minContrast)
	}
	return out
}
