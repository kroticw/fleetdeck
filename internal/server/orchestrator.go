package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// appointTimeout bounds an appointment once it has been asked for. It is
// longer than the appointment's own waits added up — a new session is given a
// minute to be listed and another to take its message — so this is the margin
// against a daemon that stops answering, not the expected time.
const appointTimeout = 3 * time.Minute

// handleOrchestratorPreview answers what an appointment would write and send,
// in the language the page asks for, so the wizard can show it before the
// person chooses.
func (d Deps) handleOrchestratorPreview(w http.ResponseWriter, r *http.Request) {
	if d.OrchestratorPreview == nil {
		unavailable(w, "an orchestrator wizard")
		return
	}
	p, err := d.OrchestratorPreview(r.URL.Query().Get("lang"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleAppoint appoints an orchestrator: a new session ({"new": true}) or an
// existing one ({"session": "<short id>"}). A refused request is answered by
// its kind; an attempted one is answered 200 with every step, a failed step
// included — the person has to see how far it got.
//
// The appointment runs on a context of its own rather than the request's: a
// page closed while a new session is starting must not leave that session
// started and never sent its message.
func (d Deps) handleAppoint(w http.ResponseWriter, r *http.Request) {
	if d.Appoint == nil {
		unavailable(w, "an orchestrator wizard")
		return
	}
	var body struct {
		New     bool   `json:"new"`
		Session string `json:"session"`
		Lang    string `json:"lang"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), appointTimeout)
	defer cancel()
	res, err := d.Appoint(ctx, orchestrator.Request{New: body.New, Session: body.Session, Lang: body.Lang})
	switch {
	case errors.Is(err, orchestrator.ErrBadRequest):
		fail(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, orchestrator.ErrBusy), errors.Is(err, orchestrator.ErrNoBoard):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, orchestrator.ErrCannotStart):
		fail(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
