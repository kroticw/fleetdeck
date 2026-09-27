package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// cardSheetPrefix is the line the board writes about an open card sheet: where
// it keeps the document it shows and the session that wrote it
// (web/js/standreport.js, cardSheetReport; T-091).
const cardSheetPrefix = "fleetdeck-window: the board reports its card sheet: "

// dockRightMin is web/js/carddock.js DOCK_RIGHT_MIN: the narrowest sheet the
// session is kept beside the document in, rather than below it.
const dockRightMin = 640

// The smallest a docked terminal and the document next to it may be and still
// be read: a few lines of a question, a few lines of the document.
const (
	minDockedW = 150
	minDockedH = 100
)

// terminalBox is the docked terminal's box, and whether the session is open in
// it rather than folded to its handle.
type terminalBox struct {
	Open bool `json:"open"`
	box
}

// cardSheet is the board's report of a card sheet; a part the sheet does not
// have is nil.
type cardSheet struct {
	Open     bool         `json:"open"`
	Stage    *box         `json:"stage"`
	Pane     *box         `json:"pane"`
	Dock     *box         `json:"dock"`
	Terminal *terminalBox `json:"terminal"`
	Place    string       `json:"place"`
	Chosen   string       `json:"chosen"`
	// Tab is the open tab's key: "card", or a document's path.
	Tab string `json:"tab"`
	// Author is whose session is docked, and where the sheet learnt who that
	// is: the document's frontmatter ("document") or the card ("card").
	Author *sheetAuthor `json:"author"`
	// Control is the session's open/fold button: on a narrow sheet the
	// handle cut it off past the sheet's edge while every box above held.
	Control *box `json:"control"`
}

type sheetAuthor struct {
	Short string `json:"short"`
	From  string `json:"from"`
}

// within is whether inner lies inside outer, to layout rounding.
func within(inner, outer box) bool {
	return inner.X >= outer.X-edgeSlack && inner.Y >= outer.Y-edgeSlack &&
		inner.X+inner.W <= outer.X+outer.W+edgeSlack && inner.Y+inner.H <= outer.Y+outer.H+edgeSlack
}

// cardSheetCheck holds the log's last card sheet report to the sheet's gates,
// when the stand opened one (FLEETDECK_STAND_OPEN=carddoc-bottom or
// carddoc-right); a stand that opened none is not held to them.
func cardSheetCheck(log string, open []string) []string {
	chosen := ""
	for _, name := range open {
		if place, ok := strings.CutPrefix(name, "carddoc-"); ok {
			chosen = place
		}
	}
	if chosen == "" {
		return nil
	}
	var last *cardSheet
	for _, line := range strings.Split(log, "\n") {
		i := strings.Index(line, cardSheetPrefix)
		if i < 0 {
			continue
		}
		var s cardSheet
		if err := json.Unmarshal([]byte(line[i+len(cardSheetPrefix):]), &s); err != nil {
			return []string{fmt.Sprintf("a card sheet report that is not one: %v", err)}
		}
		last = &s
	}
	if last == nil {
		return []string{"the board never reported a card sheet: the stand asked for a card's document with its session " + chosen}
	}
	return cardSheetVerdicts(*last, chosen)
}

// cardSheetVerdicts is what is wrong with a card sheet whose session was asked
// to be chosen ("bottom" or "right"), by the spec's gates
// (docs/superpowers/specs/2026-09-27-card-document-tabs-design.md, 8.1): the
// session open next to the document; not over it; and beside it only when the
// sheet has room, below otherwise. Nothing, when the sheet keeps all three.
func cardSheetVerdicts(s cardSheet, chosen string) []string {
	var problems []string
	// On the card's own tab, or with the card's session docked because the
	// document's author was not read, every gate below would pass for nothing.
	if s.Tab == "" || s.Tab == "card" || s.Author == nil || s.Author.From != "document" {
		problems = append(problems, fmt.Sprintf("the sheet is not on a document with its author docked: tab %q, author %v", s.Tab, s.Author))
	}
	t := s.Terminal
	if !s.Open || s.Dock == nil || t == nil || !t.Open || t.W < minDockedW || t.H < minDockedH {
		problems = append(problems, fmt.Sprintf("the session is not open next to the document: sheet open %v, session %v, terminal %v", s.Open, s.Dock, t))
	}
	if s.Pane == nil || s.Pane.W < minDockedW || s.Pane.H < minDockedH || (s.Dock != nil && overlaps(*s.Pane, *s.Dock)) {
		problems = append(problems, fmt.Sprintf("the session covers the document: document %v, session %v", s.Pane, s.Dock))
	}
	if s.Dock != nil && (s.Control == nil || !within(*s.Control, *s.Dock)) {
		problems = append(problems, fmt.Sprintf("the session's control lies outside its place: control %v, place %v", s.Control, s.Dock))
	}
	if s.Stage == nil {
		// Without the stage there is no telling where the session is, or should
		// be: the placement gate fails rather than passes by saying nothing.
		return append(problems, fmt.Sprintf("the sheet reports no stage: where the session is, %s asked, cannot be told", chosen))
	}
	switch {
	case chosen == "right" && s.Stage.W < dockRightMin && s.Place != "bottom":
		problems = append(problems, fmt.Sprintf("a narrow sheet keeps the session on the right: the sheet is %v wide, under %d", s.Stage.W, dockRightMin))
	case chosen == "right" && s.Stage.W >= dockRightMin && s.Place != "right":
		problems = append(problems, fmt.Sprintf("a wide sheet puts the session below though right was chosen: the sheet is %v wide", s.Stage.W))
	case chosen == "bottom" && s.Place != "bottom":
		problems = append(problems, fmt.Sprintf("the session is not below though below was chosen: it is %q", s.Place))
	}
	return problems
}
