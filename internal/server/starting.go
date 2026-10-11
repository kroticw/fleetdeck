package server

import (
	"slices"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/state"
)

// starts is the sessions this panel is starting right now, by card. One per
// panel, shared by every fleet and every copy of Deps: a start begun from one
// tab is drawn by every page that shows its card.
type starts struct {
	mu sync.Mutex
	by map[string]time.Time
}

func newStarts() *starts { return &starts{by: map[string]time.Time{}} }

// begin records a start for card and returns what ends it. A second start of
// the same card while the first is running keeps the first one's time: the
// dispatcher refuses it (orchestrator.ErrBusy), and the start on screen is
// still the first.
func (s *starts) begin(card string) (end func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.by[card]; running {
		return func() {}
	}
	s.by[card] = time.Now()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.by, card)
	}
}

// of is the starts under way for the cards in cards, oldest first.
func (s *starts) of(cards []string) []state.Start {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []state.Start
	for _, card := range cards {
		if since, ok := s.by[card]; ok {
			out = append(out, state.Start{Card: card, Since: since})
		}
	}
	slices.SortFunc(out, func(a, b state.Start) int { return a.Since.Compare(b.Since) })
	return out
}
