package server

import "sync"

// Notifier tells every open socket that the snapshot it would push has
// changed: a cycle published a new one, or the board's commit queue moved.
// A socket then pushes at once instead of at its next tick (T-161).
//
// Each subscriber has a channel of one: a burst of notifications while a
// socket is busy is one wake-up, not a backlog.
type Notifier struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

// NewNotifier is a Notifier with no subscribers.
func NewNotifier() *Notifier { return &Notifier{subs: map[chan struct{}]struct{}{}} }

// Subscribe returns the channel this subscriber is woken on, and what ends the
// subscription.
func (n *Notifier) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	n.subs[ch] = struct{}{}
	n.mu.Unlock()
	return ch, func() {
		n.mu.Lock()
		delete(n.subs, ch)
		n.mu.Unlock()
	}
}

// Notify wakes every subscriber that is not already due to wake.
func (n *Notifier) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
