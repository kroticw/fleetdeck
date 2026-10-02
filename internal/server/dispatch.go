package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// dispatchTimeout bounds handing a card over once it has been asked for. It is
// longer than the dispatch's own waits added up, as appointTimeout is: the
// margin against a runtime that stops answering, not the expected time.
const dispatchTimeout = 3 * time.Minute

// handleDispatch starts a session for one card and hands the card to it.
//
// It runs on a context of its own rather than the request's, for the reason
// handleAppoint does: a page closed while the session is coming up must not
// leave that session started, unnamed on the board and sent nothing.
func (d Deps) handleDispatch(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if d.StartWork == nil {
		unavailable(w, "starting a session for a card")
		return
	}
	if d.BoardDir == "" {
		unavailable(w, "a board directory")
		return
	}
	var body struct {
		Card string `json:"card"`
		Lang string `json:"lang"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Card == "" {
		fail(w, http.StatusBadRequest, "card is required: a dispatch must name the card it hands over")
		return
	}
	// Confined exactly as a card write is, and for the same reason: the path
	// arrives from the browser, and what happens to the file it names is a
	// write of its session and stage.
	card, err := confineToBoard(d.BoardDir, body.Card)
	switch {
	case errors.Is(err, errOutsideBoard):
		fail(w, http.StatusForbidden, "a dispatch is confined to the board directory")
		return
	case err != nil:
		unavailable(w, "a readable board directory")
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), dispatchTimeout)
	defer cancel()
	res, err := d.StartWork(ctx, orchestrator.Work{Card: card, Lang: body.Lang})
	switch {
	case errors.Is(err, orchestrator.ErrBadRequest):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, orchestrator.ErrBusy), errors.Is(err, orchestrator.ErrCardTaken):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, orchestrator.ErrNoCard):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, orchestrator.ErrNoRepo):
		// The request is fine and the card is not: it names no checkout a
		// worker could start in, which is fixed on the card.
		fail(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, orchestrator.ErrCannotStart):
		fail(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
