package server

import "net/http"

// handleCreateFleet makes a fleet: a folder with a board and documentation in
// it, and a line in the configuration naming them. It is the start page's one
// write (web/js/start.js).
//
// It reports the way setting up reports — the steps `fleetdeck init` prints,
// each with what it did or why it was refused — because it is the same write,
// run from a panel instead of a terminal. ok says whether the fleet is there;
// an error means the request itself was refused and nothing was made.
//
// What it deliberately does not do is make the new fleet servable. This panel
// read the configuration when it started and does not read it again: one board
// watcher per fleet, one orchestrator appointer per fleet, and the collector
// were all built from that read (cmd/fleetdeck/main.go). Rebuilding them under
// live requests is a change of its own, and a fleet that half-exists — in the
// configuration, missing from the running panel — would be worse than one that
// plainly needs a restart. The page says so, before the button and after it.
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
