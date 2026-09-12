package state

import (
	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
)

// ForFleet cuts the view one browser tab asked for out of the whole snapshot
// a collect cycle produced. The fleet is chosen by name, an empty name being
// the first fleet (fleet.Select); a name no fleet has is fleet.ErrUnknown.
//
// The view carries that fleet's cards, board error, orphan cards and
// orchestrator, with every session linked to that fleet's cards only. It keeps
// every session, not only the fleet's own: a session of another fleet is shown
// apart rather than hidden, and a session no fleet claims is shown in every
// fleet — hiding it would leave a waiting question where no tab looks. Each
// session carries the names of the fleets claiming it, from which the page
// groups them.
//
// A whole snapshot with no boards has nothing to cut and is returned as it is:
// that is the zero snapshot served before the first cycle has run.
//
// The whole snapshot is shared by every tab, so nothing reachable from it is
// modified here: the sessions are copied before they are relinked and tagged.
func ForFleet(whole Snapshot, name string) (Snapshot, error) {
	if len(whole.Boards) == 0 {
		return whole, nil
	}
	fleets := make([]fleet.Fleet, len(whole.Boards))
	names := make([]string, len(whole.Boards))
	cards := map[string][]board.Card{}
	var own FleetBoard
	for i, b := range whole.Boards {
		fleets[i], names[i] = b.Fleet, b.Fleet.Name
		cards[b.Fleet.Name] = b.Cards
	}
	chosen, err := fleet.Select(fleets, name)
	if err != nil {
		return Snapshot{}, err
	}
	for _, b := range whole.Boards {
		if b.Fleet.Name == chosen.Name {
			own = b
		}
	}

	claims := fleet.Claims(fleets, cards)
	daemonSessions := make([]daemon.Session, len(whole.Sessions))
	for i, v := range whole.Sessions {
		daemonSessions[i] = v.Session
	}
	linked := Link(daemonSessions, own.Cards)

	view := whole
	view.Boards = nil
	view.Fleet = chosen.Name
	view.Fleets = names
	view.Cards = own.Cards
	view.BoardError = own.BoardError
	view.OrchestratorSession = chosen.Orchestrator
	view.OrchestratorBriefPath = own.BriefPath
	view.OrchestratorBriefMissing = own.BriefMissing
	// Both card lists are computed from the whole snapshot's own views, not
	// from daemonSessions: the lifecycle of a session is carried by the view
	// (see SessionView.Lifecycle), and a daemon.Session alone cannot say
	// whether a session is stopped, dead or running.
	view.OrphanCards = OrphanCards(whole.Sessions, own.Cards)
	view.StoppedCards = StoppedCards(whole.Sessions, own.Cards)
	view.Sessions = make([]SessionView, len(whole.Sessions))
	for i, v := range whole.Sessions {
		v.CardPath, v.CardID = linked[i].CardPath, linked[i].CardID
		v.Fleets = claims[v.Short]
		view.Sessions[i] = v
	}
	return view, nil
}
