// internal/state/events_test.go
package state

import (
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

func waitingView(short string) SessionView {
	return SessionView{Session: daemon.Session{Short: short, Name: "session " + short, Needs: "answer: pick one (A · B)"}}
}

func idleView(short string) SessionView {
	return SessionView{Session: daemon.Session{Short: short, Name: "session " + short}}
}

func stalledView(short string) SessionView {
	return SessionView{Session: daemon.Session{Short: short, Name: "session " + short, State: "blocked", Detail: "waiting on my own subagents"}}
}

func TestSessionBecomingWaitingFiresOnce(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{waitingView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:waiting" {
		t.Fatalf("a session that started waiting must fire once: %+v", fire)
	}
	if len(cleared) != 0 {
		t.Fatalf("a fresh transition into waiting must not also clear something: %v", cleared)
	}
}

func TestStillWaitingDoesNotFireAgain(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{Sessions: []SessionView{waitingView("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a standing state is not a new event: %+v", fire)
	}
}

func TestSessionThatStoppedWaitingIsCleared(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{Sessions: []SessionView{idleView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a session leaving waiting must not itself fire: %+v", fire)
	}
	if len(cleared) != 1 || cleared[0] != "session:a:waiting" {
		t.Fatalf("a resolved state must be cleared so it can fire again: %v", cleared)
	}
}

// TestSessionBecomingStalledDoesNotFire records the deliberate design
// decision: Stalled() has no notification rule of its own (see the doc
// comment on Diff). A session moving straight from idle into Stalled
// (state=blocked, no words in Needs) must produce no banner at all.
func TestSessionBecomingStalledDoesNotFire(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{stalledView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("becoming Stalled must not fire any banner: %+v", fire)
	}
	if len(cleared) != 0 {
		t.Fatalf("there was no prior waiting state to clear: %v", cleared)
	}
}

// TestWaitingSessionThatBecomesStalledClearsWaitingButFiresNothingNew checks
// the boundary between the two: leaving Waiting still clears the waiting key
// (so it can fire again later), but arriving at Stalled fires nothing, since
// Stalled has no banner of its own.
func TestWaitingSessionThatBecomesStalledClearsWaitingButFiresNothingNew(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{waitingView("a")}}
	next := Snapshot{Sessions: []SessionView{stalledView("a")}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("Stalled has no banner of its own, even right after Waiting cleared: %+v", fire)
	}
	if len(cleared) != 1 || cleared[0] != "session:a:waiting" {
		t.Fatalf("the resolved waiting state must still be cleared: %v", cleared)
	}
}

func TestSessionEndingInFailureFires(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{idleView("a")}}
	failed := idleView("a")
	failed.State = "failed"
	next := Snapshot{Sessions: []SessionView{failed}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:failed" {
		t.Fatalf("a session ending in failure must fire once: %+v", fire)
	}
}

func TestStillFailedDoesNotFireAgain(t *testing.T) {
	failed := idleView("a")
	failed.State = "failed"
	prev := Snapshot{Sessions: []SessionView{failed}}
	next := Snapshot{Sessions: []SessionView{failed}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a standing failure is not a new event: %+v", fire)
	}
}

func TestFailedSessionThatRecoversIsCleared(t *testing.T) {
	failed := idleView("a")
	failed.State = "failed"
	prev := Snapshot{Sessions: []SessionView{failed}}
	next := Snapshot{Sessions: []SessionView{idleView("a")}}
	_, cleared := Diff(prev, next, time.Hour)
	if len(cleared) != 1 || cleared[0] != "session:a:failed" {
		t.Fatalf("a session leaving the failed state must be cleared: %v", cleared)
	}
}

func TestSilenceFiresOnceOnCrossingTheThreshold(t *testing.T) {
	before := idleView("a")
	before.SilentFor = 10 * time.Minute
	after := idleView("a")
	after.SilentFor = 31 * time.Minute
	prev := Snapshot{Sessions: []SessionView{before}}
	next := Snapshot{Sessions: []SessionView{after}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 1 || fire[0].Key != "session:a:silent" {
		t.Fatalf("crossing the silence threshold must fire: %+v", fire)
	}
}

func TestSilenceDoesNotFireAgainWhileStillOverThreshold(t *testing.T) {
	still := idleView("a")
	still.SilentFor = 45 * time.Minute
	prev := Snapshot{Sessions: []SessionView{still}}
	next := Snapshot{Sessions: []SessionView{still}}
	fire, _ := Diff(prev, next, 30*time.Minute)
	if len(fire) != 0 {
		t.Fatalf("staying silent past the threshold is a standing state, not a new event: %+v", fire)
	}
}

func TestSilenceClearsWhenActivityResumes(t *testing.T) {
	before := idleView("a")
	before.SilentFor = 45 * time.Minute
	after := idleView("a")
	after.SilentFor = 0
	prev := Snapshot{Sessions: []SessionView{before}}
	next := Snapshot{Sessions: []SessionView{after}}
	_, cleared := Diff(prev, next, 30*time.Minute)
	if len(cleared) != 1 || cleared[0] != "session:a:silent" {
		t.Fatalf("resumed activity must clear the silence banner so it can fire again: %v", cleared)
	}
}

func TestCardEnteringBlockedFires(t *testing.T) {
	prev := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "blocked"}}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "card:/board/c.md:blocked" {
		t.Fatalf("a card entering blocked must fire: %+v", fire)
	}
}

func TestCardEnteringReviewFires(t *testing.T) {
	prev := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "review"}}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "card:/board/c.md:review" {
		t.Fatalf("a card entering review must fire: %+v", fire)
	}
}

func TestCardMovingBetweenNonNotifiableStagesFiresNothing(t *testing.T) {
	prev := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "new"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	fire, cleared := Diff(prev, next, time.Hour)
	if len(fire) != 0 || len(cleared) != 0 {
		t.Fatalf("new -> active is not one of the two notifiable stages: fire=%+v cleared=%v", fire, cleared)
	}
}

func TestCardLeavingBlockedIsCleared(t *testing.T) {
	prev := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "blocked"}}}
	next := Snapshot{Cards: []board.Card{{Path: "/board/c.md", Stage: "active"}}}
	_, cleared := Diff(prev, next, time.Hour)
	if len(cleared) != 1 || cleared[0] != "card:/board/c.md:blocked" {
		t.Fatalf("a card leaving blocked must be cleared: %v", cleared)
	}
}

// TestCardBrandNewInBlockedDoesNotFire: a card this diff has never seen
// before has no prior stage to compare against. Unlike a session (see
// TestSessionBrandNewAlreadyWaitingDoesFire below), there is no way to tell
// "just moved to blocked" apart from "created directly in blocked" for a
// card, so a first sighting stays quiet rather than guess.
func TestCardBrandNewInBlockedDoesNotFire(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{idleView("z")}}
	next := Snapshot{
		Sessions: []SessionView{idleView("z")},
		Cards:    []board.Card{{Path: "/board/new.md", Stage: "blocked"}},
	}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 0 {
		t.Fatalf("a brand-new card must not fire even if already blocked: %+v", fire)
	}
}

// TestSessionBrandNewAlreadyWaitingDoesFire: unlike a card, a session that
// appears for the first time already Waiting is itself news the operator has
// not been told about yet, so it fires even though this particular session
// has no prior entry to compare against.
func TestSessionBrandNewAlreadyWaitingDoesFire(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{idleView("z")}}
	next := Snapshot{Sessions: []SessionView{idleView("z"), waitingView("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Key != "session:a:waiting" {
		t.Fatalf("a session appearing already waiting must fire: %+v", fire)
	}
}

func TestEmptyPreviousSnapshotFiresNothing(t *testing.T) {
	next := Snapshot{Sessions: []SessionView{waitingView("a"), waitingView("b")}}
	fire, cleared := Diff(Snapshot{}, next, time.Hour)
	if len(fire) != 0 || len(cleared) != 0 {
		t.Fatalf("the first snapshot has nothing to compare against and must stay quiet, got fire=%+v cleared=%v", fire, cleared)
	}
}

func TestEventTextIsEnglish(t *testing.T) {
	prev := Snapshot{Sessions: []SessionView{idleView("a")}}
	next := Snapshot{Sessions: []SessionView{waitingView("a")}}
	fire, _ := Diff(prev, next, time.Hour)
	if len(fire) != 1 || fire[0].Text != "is waiting for an answer" {
		t.Fatalf("interface text must be English per spec section 10.1: %+v", fire)
	}
}
