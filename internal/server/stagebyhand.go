package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// What happens around a stage the operator sets, and why it happens here.
//
// This route is the one place a stage is written by a hand. The agent keeping a
// card writes the file with its own editor and never speaks HTTP at all, and
// the panel's own stage write on a dispatch goes straight to the board writer
// (internal/orchestrator.Dispatcher) — so everything that arrives here is the
// operator, and nothing that arrives here is an agent hearing its own echo.
// That is the whole of the distinction the announcement below needs, and it is
// structural rather than a flag somebody has to remember to set.

const (
	// doneStage is the stage that ends a card's life on the board. Written here
	// rather than taken from internal/board because this is not the board's
	// vocabulary being validated — that already happened — but this package's
	// one rule about one of its values.
	doneStage = "done"

	// handTimeout bounds everything done after the card is written: reaching
	// the session, and tidying it away. Longer than the reaches inside it added
	// up, as the dispatch's own timeout is: the margin against a runtime that
	// stops answering, not the expected time.
	handTimeout = 2 * time.Minute
)

// answerCardWrite answers a write that took effect. steps, when there are any,
// are what happened around it — a session told, a session tidied away — and
// they turn a bare 204 into an answer carrying them, because a cleanup nobody
// can read is a cleanup the operator has to go and verify by hand.
//
// committed and reason are the git history's half of the same write: a field in
// the file with no commit behind it is still a success (see the route).
func (d Deps) answerCardWrite(w http.ResponseWriter, steps []orchestrator.Step, committed bool, reason string) {
	if len(steps) == 0 && committed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	out := map[string]any{"written": true, "committed": committed}
	if reason != "" {
		out["reason"] = reason
	}
	if len(steps) > 0 {
		out["steps"] = steps
	}
	writeJSON(w, http.StatusOK, out)
}

// stageByHand is everything that follows a stage the operator set on a card
// somebody is keeping: the session is told, and a card moved into done has its
// session tidied away. The session is told in lang, the page's language.
//
// Nothing here can fail the write. The card is already on disk in the stage the
// operator chose, and a panel that reported a failure over a message it could
// not deliver would invite them to do the move again.
//
// It runs on a context of its own rather than the request's, for the reason
// handleDispatch does: a page closed while a session is being put out must not
// leave it half tidied away.
func (d Deps) stageByHand(r *http.Request, before board.Card, field, value, lang string) []orchestrator.Step {
	if field != "stage" || before.ParseError != "" || before.Session == "" || before.Stage == value {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), handTimeout)
	defer cancel()

	var steps []orchestrator.Step
	if d.SendToSession != nil {
		text := orchestrator.StageByHand(lang, before.Path, before.Stage, value)
		if err := d.SendToSession(ctx, before.Session, text); err != nil {
			// Not an error about the board: the card moved. What the operator
			// has to know is that the agent is still working from the stage it
			// wrote, and may write it back.
			steps = append(steps, orchestrator.Step{
				Name:  "message",
				Error: fmt.Sprintf("%s was not told its card moved: %v", before.Session, err),
			})
		}
	}
	if value != doneStage || d.CleanupSession == nil {
		return steps
	}
	// After the message, deliberately: a session that has been put out cannot be
	// told anything, and being told its card was accepted is the last thing it
	// is any use hearing.
	res, err := d.CleanupSession(ctx, orchestrator.Accepted{Session: before.Session, Card: before.Path})
	if err != nil {
		return append(steps, orchestrator.Step{Name: "cleanup", Error: err.Error()})
	}
	return append(steps, res.Steps...)
}

// cardBefore is the card as it stands before the write, read for the two things
// only the file can answer: which session is keeping it, and which stage the
// operator is moving it away from. A card that cannot be read comes back with
// its ParseError set and announces nothing — the write into it is refused by
// internal/board anyway.
func cardBefore(path string) board.Card {
	c, err := board.ParseCard(path)
	if err != nil {
		return board.Card{Path: path, ParseError: err.Error()}
	}
	return c
}
