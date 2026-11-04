package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/executor"
)

// handleSessionRequests lists what a session is waiting on: approvals and
// questions, as the runtime describes them, never raw JSON-RPC.
func (d Deps) handleSessionRequests(w http.ResponseWriter, r *http.Request) {
	if d.SessionRequests == nil {
		unavailable(w, "Codex")
		return
	}
	list, err := d.SessionRequests(r.Context(), r.PathValue("id"))
	if err != nil {
		requestFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRespondRequest answers one request with the operator's explicit
// choice. The body is only a token and the answer; nothing else is passed on.
func (d Deps) handleRespondRequest(w http.ResponseWriter, r *http.Request) {
	if d.RespondRequest == nil {
		unavailable(w, "Codex")
		return
	}
	var a codexruntime.Answer
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&a); err != nil || a.Token == "" {
		fail(w, http.StatusBadRequest, "an answer needs a request token and a decision or answers")
		return
	}
	if err := d.RespondRequest(r.Context(), r.PathValue("id"), a); err != nil {
		requestFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requestFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, executor.ErrUnsupported):
		fail(w, http.StatusNotFound, "this session has no requests to answer here")
	case errors.Is(err, executor.ErrConflict):
		fail(w, http.StatusConflict, "this request was already answered or replaced; reload the requests")
	case errors.Is(err, codexruntime.ErrInvalidFrame):
		fail(w, http.StatusBadRequest, "this answer does not fit the request")
	case errors.Is(err, codexruntime.ErrReplyUnknown):
		fail(w, http.StatusBadGateway, "the answer may have reached Codex; do not answer again, reload the requests")
	default:
		fail(w, http.StatusBadGateway, err.Error())
	}
}
