//go:build darwin

package main

import "sync"

// callQueue answers one surface web view's binding calls one at a time, in the
// order its page made them. Each call used to be answered on a goroutine of its
// own, so a fold and an unfold sent back to back could be saved in either
// order. A call waits on the one before it; nothing waits on the main thread,
// which only pushes.
type callQueue struct {
	mu      sync.Mutex
	pending []string
	closed  bool
	wake    chan struct{}
	done    chan struct{}
}

func newCallQueue(answer func(message string)) *callQueue {
	q := &callQueue{wake: make(chan struct{}, 1), done: make(chan struct{})}
	go q.run(answer)
	return q
}

// push queues a call; a call pushed after close is dropped.
func (q *callQueue) push(message string) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.pending = append(q.pending, message)
	q.mu.Unlock()
	q.nudge()
}

// close drops the calls not yet answered. The call being answered, if any,
// finishes; close does not wait for it.
func (q *callQueue) close() {
	q.mu.Lock()
	q.closed, q.pending = true, nil
	q.mu.Unlock()
	q.nudge()
}

func (q *callQueue) nudge() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *callQueue) run(answer func(message string)) {
	defer close(q.done)
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			closed := q.closed
			q.mu.Unlock()
			if closed {
				return
			}
			<-q.wake
			continue
		}
		message := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()
		answer(message)
	}
}

// replyGoesTo says whether a reply to a call the surface asked made may be
// evaluated in the surface of kind now: only while that is still the same web
// view. A surface made again, after a fleet switch, numbers its calls from 1
// again, and a late reply to the old page would settle a call of the new one.
func replyGoesTo(surfaces map[string]*surface, kind string, asked *surface) bool {
	return asked != nil && surfaces[kind] == asked
}
