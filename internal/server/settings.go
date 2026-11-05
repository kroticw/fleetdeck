package server

import (
	"context"
	"errors"
	"net/http"
)

// ErrBadSettings is a settings write the configuration refuses: an unknown
// key or a value out of its range. The route answers 400 with its reason.
var ErrBadSettings = errors.New("settings refused")

func (d Deps) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if d.Settings == nil {
		unavailable(w, "a configuration store")
		return
	}
	page, err := d.Settings(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (d Deps) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	if d.WriteSettings == nil {
		unavailable(w, "a configuration store")
		return
	}
	var values map[string]any
	if !decodeBody(w, r, &values) {
		return
	}
	page, err := d.WriteSettings(context.WithoutCancel(r.Context()), values)
	switch {
	case errors.Is(err, ErrBadSettings):
		fail(w, http.StatusBadRequest, err.Error())
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		// The snapshot carries the look; made again now, it does not take
		// the page back to the old theme until the next poll.
		if d.Changed != nil {
			d.Changed()
		}
		writeJSON(w, http.StatusOK, page)
	}
}
