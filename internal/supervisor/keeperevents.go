package supervisor

import (
	"context"
	"sync"
	"sync/atomic"
)

// KeeperEvents is how a window hears its keeper (Keeper.OnEvent = Push). Every
// event is kept for the window until it can act on them (Run) and handed on
// exactly once, in the order the keeper said them: the keeper is made before
// the window's web views, and a panel may start or fail before there is a page
// to show that in. While a takeover runs (Take), every event pushed is also
// kept for the takeover, whether or not the window is reading yet: a takeover
// that did not hear its own panel answer would fail the update by the old
// window's deadline. Push never blocks the keeper, and neither route loses an
// event.
type KeeperEvents struct {
	window   *eventQueue
	takeover atomic.Pointer[eventQueue]
}

// NewKeeperEvents is a KeeperEvents with nothing pushed yet.
func NewKeeperEvents() *KeeperEvents {
	return &KeeperEvents{window: newEventQueue()}
}

// Push keeps e for the window and, while one runs, for the takeover.
func (k *KeeperEvents) Push(e Event) {
	if q := k.takeover.Load(); q != nil {
		q.push(e)
	}
	k.window.push(e)
}

// Run hands handle every event pushed so far and every one pushed after, in
// order, until ctx is done.
func (k *KeeperEvents) Run(ctx context.Context, handle func(Event)) {
	k.window.run(ctx, func(e Event) bool {
		handle(e)
		return true
	})
}

// Take runs t with every event pushed from now on as its Events, and returns
// what t.Run returns. The route goes when t.Run has returned.
func (k *KeeperEvents) Take(ctx context.Context, t *Takeover) error {
	events, stop := k.route(ctx)
	defer stop()
	t.Events = events
	return t.Run(ctx)
}

// route is the takeover's channel of every event pushed from now on, fed
// without loss however long the takeover leaves it unread, and the function
// that ends it.
func (k *KeeperEvents) route(ctx context.Context) (<-chan Event, func()) {
	q := newEventQueue()
	events := make(chan Event)
	feed, cancel := context.WithCancel(ctx)
	k.takeover.Store(q)
	go q.run(feed, func(e Event) bool {
		select {
		case events <- e:
			return true
		case <-feed.Done():
			return false
		}
	})
	return events, func() {
		k.takeover.CompareAndSwap(q, nil)
		cancel()
	}
}

// eventQueue holds events in order and hands each on once. push never blocks.
type eventQueue struct {
	mu      sync.Mutex
	pending []Event
	wake    chan struct{}
}

func newEventQueue() *eventQueue {
	return &eventQueue{wake: make(chan struct{}, 1)}
}

func (q *eventQueue) push(e Event) {
	q.mu.Lock()
	q.pending = append(q.pending, e)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// run hands handle each event in order until ctx is done or handle says to
// stop.
func (q *eventQueue) run(ctx context.Context, handle func(Event) bool) {
	for {
		q.mu.Lock()
		batch := q.pending
		q.pending = nil
		q.mu.Unlock()
		for _, e := range batch {
			if !handle(e) {
				return
			}
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return
		}
	}
}
