//go:build darwin

package main

import (
	"context"
	"sync"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// keeperQueue holds the keeper's events until the window can act on them, and
// hands each on exactly once, in the order the keeper said them. The keeper is
// created before the window's web views, and a panel may start -- or fail --
// before there is a page to show that in; nothing it says may be lost to that.
// push never blocks the keeper.
type keeperQueue struct {
	mu      sync.Mutex
	pending []supervisor.Event
	wake    chan struct{}
}

func newKeeperQueue() *keeperQueue {
	return &keeperQueue{wake: make(chan struct{}, 1)}
}

// push keeps e for run.
func (q *keeperQueue) push(e supervisor.Event) {
	q.mu.Lock()
	q.pending = append(q.pending, e)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// run hands handle every event pushed so far and every one pushed after, in
// order, until ctx is done.
func (q *keeperQueue) run(ctx context.Context, handle func(supervisor.Event)) {
	for {
		q.mu.Lock()
		batch := q.pending
		q.pending = nil
		q.mu.Unlock()
		for _, e := range batch {
			handle(e)
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return
		}
	}
}
