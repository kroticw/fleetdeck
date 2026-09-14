package main

// The capsules are drawn once, on the main thread, in TestMain
// (menu_darwin_test.go), on a window never put on screen; the tests below only
// read what was found.

import (
	"encoding/json"
	"reflect"
	"testing"
)

var capsulesResult capsulesProbe

func collectCapsuleResults() {
	m, err := parseCapsuleModel(json.RawMessage(`{"version":1,"tabs":[{"id":"board","label":"Доска","selected":true},{"id":"docs","label":"Доки","selected":false}],"newCard":{"label":"+ карточка"},"theme":{"label":"тема: авто"},"limits":[{"label":"5ч","text":"37%","level":"cool","color":"#2f9e44"}]}`))
	if err != nil {
		panic(err)
	}
	capsulesResult = probeCapsulesForTest(m)
}

func TestCapsulesDrawThePagesModel(t *testing.T) {
	r := capsulesResult
	if r.segmentLabels != [2]string{"Доска", "Доки"} || r.selectedSegment != 0 {
		t.Fatalf("segments = %v selected %d", r.segmentLabels, r.selectedSegment)
	}
	if r.newCardTitle != "+ карточка" || r.themeTitle != "тема: авто" {
		t.Fatalf("buttons = %q %q", r.newCardTitle, r.themeTitle)
	}
	if r.levelValue != 37 {
		t.Fatalf("level = %v", r.levelValue)
	}
	if r.capsuleCount != 4 {
		t.Fatalf("capsules = %d, want tabs, new card, theme, one limit", r.capsuleCount)
	}
}

func TestADrawnAgainRowReplacesTheCapsulesInsteadOfAddingThem(t *testing.T) {
	if capsulesResult.countAfterRedraw != 4 {
		t.Fatalf("after a second draw: %d, want the same four capsules in one row", capsulesResult.countAfterRedraw)
	}
}

func TestAPressInTheRowBecomesTheControllersAction(t *testing.T) {
	if !reflect.DeepEqual(capsulesResult.presses, []string{"tab:docs", "newCard"}) {
		t.Fatalf("presses = %v", capsulesResult.presses)
	}
}

func TestAClickInTheGapBetweenCapsulesReachesTheBoard(t *testing.T) {
	if !capsulesResult.rowPassesThrough {
		t.Fatal("the capsule row's container takes a click between capsules, over the board")
	}
}
