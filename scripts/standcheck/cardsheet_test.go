package main

import (
	"strings"
	"testing"
)

// A card sheet as the board reports it when its tabs and the author's session
// are where they belong: the document on the left, the session beside it.
func goodSheet() cardSheet {
	return cardSheet{
		Open:     true,
		Stage:    &box{X: 400, Y: 120, W: 820, H: 560},
		Pane:     &box{X: 400, Y: 120, W: 450, H: 560},
		Dock:     &box{X: 858, Y: 120, W: 362, H: 560},
		Terminal: &terminalBox{Open: true, box: box{X: 858, Y: 160, W: 362, H: 480}},
		Place:    "right",
		Chosen:   "right",
		Tab:      "/stand/board/docs/reports/q.md",
		Author:   &sheetAuthor{Short: "5e55a002", From: "document"},
	}
}

// The sheet has to be on a document's tab with that document's author docked:
// on the card's own tab, or with the card's session docked because the
// document's author was not read, every other gate would pass for nothing.
func TestASheetNotOnItsDocumentWithItsAuthorIsCaught(t *testing.T) {
	for name, edit := range map[string]func(*cardSheet){
		"the card's tab":          func(s *cardSheet) { s.Tab = "card" },
		"no tab":                  func(s *cardSheet) { s.Tab = "" },
		"the card's session":      func(s *cardSheet) { s.Author.From = "card" },
		"nobody's session docked": func(s *cardSheet) { s.Author = nil },
	} {
		s := goodSheet()
		edit(&s)
		if got := strings.Join(cardSheetVerdicts(s, "right"), "\n"); !strings.Contains(got, "the sheet is not on a document with its author docked") {
			t.Errorf("%s: verdicts %q", name, got)
		}
	}
}

func TestACardSheetWithItsSessionBesideTheDocumentHolds(t *testing.T) {
	if got := cardSheetVerdicts(goodSheet(), "right"); len(got) != 0 {
		t.Fatalf("verdicts %v", got)
	}
}

func TestACardSheetWithoutItsSessionIsCaught(t *testing.T) {
	for name, edit := range map[string]func(*cardSheet){
		"no session place":    func(s *cardSheet) { s.Dock = nil; s.Terminal = nil },
		"no terminal":         func(s *cardSheet) { s.Terminal = nil },
		"terminal folded":     func(s *cardSheet) { s.Terminal.Open = false },
		"terminal too narrow": func(s *cardSheet) { s.Terminal.W = 149 },
		"terminal too low":    func(s *cardSheet) { s.Terminal.H = 99 },
		"sheet without panes": func(s *cardSheet) { s.Pane, s.Dock, s.Terminal = nil, nil, nil },
		"sheet closed":        func(s *cardSheet) { s.Open = false },
	} {
		s := goodSheet()
		edit(&s)
		got := cardSheetVerdicts(s, "right")
		if !strings.Contains(strings.Join(got, "\n"), "the session is not open next to the document") {
			t.Errorf("%s: verdicts %v", name, got)
		}
	}
}

// The least a docked terminal and its document may be: what a 1000 pt window's
// sheet between two open panels leaves them (about 230 wide, less its padding).
func TestADockedSessionAtItsLeastSizeHolds(t *testing.T) {
	s := goodSheet()
	s.Terminal.W, s.Terminal.H = 150, 100
	s.Pane.W, s.Pane.H = 150, 100
	if got := cardSheetVerdicts(s, "right"); len(got) != 0 {
		t.Fatalf("verdicts %v", got)
	}
}

func TestASessionOverTheDocumentIsCaught(t *testing.T) {
	over := goodSheet()
	over.Dock.X = 840 // 10 px into the document
	squeezed := goodSheet()
	squeezed.Pane.W = 149
	for name, s := range map[string]cardSheet{"overlapping": over, "document squeezed": squeezed} {
		if got := strings.Join(cardSheetVerdicts(s, "right"), "\n"); !strings.Contains(got, "the session covers the document") {
			t.Errorf("%s: verdicts %q", name, got)
		}
	}
	touching := goodSheet()
	touching.Dock.X = 850 // edge to edge is not over it
	if got := cardSheetVerdicts(touching, "right"); len(got) != 0 {
		t.Errorf("touching: verdicts %v", got)
	}
}

func TestASessionAskedRightGoesBelowOnANarrowSheetOnly(t *testing.T) {
	narrow := goodSheet()
	narrow.Stage.W = 639
	if got := strings.Join(cardSheetVerdicts(narrow, "right"), "\n"); !strings.Contains(got, "a narrow sheet keeps the session on the right") {
		t.Errorf("639 wide, right: %q", got)
	}
	narrow.Place = "bottom"
	narrow.Pane = &box{X: 400, Y: 120, W: 639, H: 250}
	narrow.Dock = &box{X: 400, Y: 378, W: 639, H: 302}
	narrow.Terminal = &terminalBox{Open: true, box: box{X: 400, Y: 410, W: 639, H: 230}}
	if got := cardSheetVerdicts(narrow, "right"); len(got) != 0 {
		t.Errorf("639 wide, bottom: %v", got)
	}
	wide := goodSheet()
	wide.Stage.W = 640
	wide.Place = "bottom"
	if got := strings.Join(cardSheetVerdicts(wide, "right"), "\n"); !strings.Contains(got, "a wide sheet puts the session below though right was chosen") {
		t.Errorf("640 wide, bottom: %q", got)
	}
	below := goodSheet()
	below.Chosen, below.Place = "bottom", "right"
	if got := strings.Join(cardSheetVerdicts(below, "bottom"), "\n"); !strings.Contains(got, "the session is not below though below was chosen") {
		t.Errorf("bottom chosen, right taken: %q", got)
	}
}

// A sheet that reports no stage cannot say where its session is: that is a
// failure of the placement gate, not a pass by silence.
func TestASheetWithoutAStageFailsThePlacementGate(t *testing.T) {
	s := goodSheet()
	s.Stage = nil
	for _, chosen := range []string{"right", "bottom"} {
		if got := strings.Join(cardSheetVerdicts(s, chosen), "\n"); !strings.Contains(got, "the sheet reports no stage") {
			t.Errorf("%s: verdicts %q", chosen, got)
		}
	}
}

func TestTheLastCardSheetReportInTheLogIsTheOneHeld(t *testing.T) {
	log := strings.Join([]string{
		cardSheetPrefix + `{"report":"cardSheet","open":true,"stage":null,"pane":null,"dock":null,"terminal":null,"place":null,"chosen":null}`,
		cardSheetPrefix + `{"report":"cardSheet","open":true,"stage":{"x":400,"y":120,"w":820,"h":560},"pane":{"x":400,"y":120,"w":450,"h":560},` +
			`"dock":{"x":858,"y":120,"w":362,"h":560},"terminal":{"open":true,"x":858,"y":160,"w":362,"h":480},"place":"right","chosen":"right",` +
			`"tab":"/stand/board/docs/reports/q.md","author":{"short":"5e55a002","from":"document"}}`,
	}, "\n")
	if got := cardSheetCheck(log, []string{"carddoc-right"}); len(got) != 0 {
		t.Fatalf("verdicts %v", got)
	}
}

func TestAStandThatOpenedACardSheetNeedsItsReport(t *testing.T) {
	got := strings.Join(cardSheetCheck("fleetdeck-window: nothing else\n", []string{"carddoc-bottom"}), "\n")
	if !strings.Contains(got, "the board never reported a card sheet") {
		t.Fatalf("verdicts %q", got)
	}
	if got := cardSheetCheck("", []string{"session"}); len(got) != 0 {
		t.Fatalf("a stand that opened no card sheet is not held to one: %v", got)
	}
}
