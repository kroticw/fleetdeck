package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// ErrOrchestratorTaken is a pin refused because the session is already
// another fleet's orchestrator: one session leading two fleets would read two
// boards. The route answers it with 409, the reason in the body.
var ErrOrchestratorTaken = errors.New("the session is another fleet's orchestrator")

// FleetDeps are the capabilities that differ from one fleet to the next: its
// board, its documentation, where a new card goes, its orchestrator pin and
// its orchestrator wizard. Each field means what the Deps field of the same
// name means, for that fleet.
type FleetDeps struct {
	BoardDir               string
	DocsRoots              []string
	CreateCard             func(title, zone string) (string, error)
	SetOrchestratorSession func(id string) error
	OrchestratorPreview    func(lang string) (orchestrator.Preview, error)
	Appoint                func(ctx context.Context, req orchestrator.Request) (orchestrator.Result, error)
}

// forFleet is d with the capabilities of the fleet the request names in its
// fleet query parameter — the first fleet when it names none — in place of
// d's own. The fleet lives in the tab's address, as it does for the snapshot
// (see fleetView), so a card started, a document opened or an orchestrator
// pinned in one tab lands in that tab's fleet whatever another tab shows. A
// panel wired without Fleet has one fleet, d's own fields. A fleet no
// configuration has is answered 404 here, and false tells the handler to stop.
func (d Deps) forFleet(w http.ResponseWriter, r *http.Request) (Deps, bool) {
	if d.Fleet == nil {
		return d, true
	}
	f, err := d.Fleet(r.URL.Query().Get("fleet"))
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return d, false
	}
	d.BoardDir = f.BoardDir
	d.DocsRoots = f.DocsRoots
	d.CreateCard = f.CreateCard
	d.SetOrchestratorSession = f.SetOrchestratorSession
	d.OrchestratorPreview = f.OrchestratorPreview
	d.Appoint = f.Appoint
	return d, true
}
