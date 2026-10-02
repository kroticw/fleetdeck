package server

import (
	"net/http"
	"strings"
)

// handleCreateFleet makes a fleet: a folder with a board and documentation in
// it, and a line in the configuration naming them. It is one of the start
// page's two writes (web/js/start.js); handleDeleteFleet is the other.
//
// It reports the way setting up reports — the steps `fleetdeck init` prints,
// each with what it did or why it was refused — because it is the same write,
// run from a panel instead of a terminal. ok says whether the fleet is there;
// an error means the request itself was refused and nothing was made.
//
// ok also means the fleet is served: the steps end with the running panel
// taking the fleet in (cmd/fleetdeck/main.go, fleetMaker), and a fleet it
// cannot take is a failed step, never a made fleet — one that is in the
// configuration and answers 404 is what the page must not call made.
func (d Deps) handleCreateFleet(w http.ResponseWriter, r *http.Request) {
	if d.CreateFleet == nil {
		unavailable(w, "making fleets")
		return
	}
	var body struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	steps, ok, err := d.CreateFleet(body.Name, body.Path)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "steps": steps})
}

// handleDeleteFleet deletes the listed fleet the path names (Deps.DeleteFleet).
// A refusal is 409 in the deleter's own words: the request was understood, and
// the fleet's state is what stands in the way.
func (d Deps) handleDeleteFleet(w http.ResponseWriter, r *http.Request) {
	if d.DeleteFleet == nil {
		unavailable(w, "deleting fleets")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		fail(w, http.StatusBadRequest, "name the fleet to delete")
		return
	}
	kept, err := d.DeleteFleet(name)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	if kept == nil {
		kept = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "kept": kept})
}
