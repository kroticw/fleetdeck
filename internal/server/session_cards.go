package server

import (
	"errors"
	"net/http"

	"github.com/kroticw/fleetdeck/internal/board"
)

// handleSessionCards serves the cards a session has worked on, archived ones
// included, oldest first (board.SessionCards). id is the daemon's short id: it
// is what a card's session field holds.
//
// It is a route of its own rather than a part of the snapshot. The snapshot
// reads the board's cards and never its archive, which is where closed work
// goes, and the history is wanted only while somebody has one session open —
// not pushed to every tab once a second.
func (d Deps) handleSessionCards(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if d.BoardDir == "" {
		unavailable(w, "a board")
		return
	}
	cards, err := board.SessionCards(d.BoardDir, r.PathValue("id"))
	switch {
	case errors.Is(err, board.ErrNoCardsDir):
		fail(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, cards)
	}
}
