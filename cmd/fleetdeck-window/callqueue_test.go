//go:build darwin

package main

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestASurfacesCallsAreAnsweredOneAtATimeInTheOrderMade(t *testing.T) {
	const calls = 50
	var (
		mu       sync.Mutex
		answered []string
		running  atomic.Int32
		overlap  atomic.Bool
	)
	all := make(chan struct{})
	q := newCallQueue(func(message string) {
		if running.Add(1) > 1 {
			overlap.Store(true)
		}
		// The first calls slower than the later ones: separate goroutines
		// would finish them out of order.
		n, _ := strconv.Atoi(message)
		if n < 5 {
			time.Sleep(time.Duration(5-n) * time.Millisecond)
		}
		mu.Lock()
		answered = append(answered, message)
		done := len(answered) == calls
		mu.Unlock()
		running.Add(-1)
		if done {
			close(all)
		}
	})
	defer q.close()
	for i := 0; i < calls; i++ {
		q.push(strconv.Itoa(i))
	}
	select {
	case <-all:
	case <-time.After(5 * time.Second):
		t.Fatal("the calls were not all answered")
	}
	if overlap.Load() {
		t.Fatal("two calls of one surface were answered at the same time")
	}
	for i, m := range answered {
		if m != strconv.Itoa(i) {
			t.Fatalf("answered %v, want the order the calls were made", answered)
		}
	}
}

func TestAClosedQueueDropsItsCallsAndStops(t *testing.T) {
	var answered atomic.Int32
	block := make(chan struct{})
	q := newCallQueue(func(string) {
		<-block
		answered.Add(1)
	})
	q.push("being answered")
	q.push("waiting")
	// The first call is taken, and blocks; the second waits behind it.
	time.Sleep(20 * time.Millisecond)
	q.close()
	q.push("after close")
	close(block)
	select {
	case <-q.done:
	case <-time.After(5 * time.Second):
		t.Fatal("a closed queue did not stop")
	}
	if got := answered.Load(); got != 1 {
		t.Fatalf("answered %d calls, want only the one being answered when the queue closed", got)
	}
}

func TestAReplyGoesOnlyToTheSurfaceThatMadeTheCall(t *testing.T) {
	old, remade := &surface{kind: "sessions"}, &surface{kind: "sessions"}
	surfaces := map[string]*surface{"sessions": old}
	if !replyGoesTo(surfaces, "sessions", old) {
		t.Fatal("a reply to the surface still shown is not evaluated")
	}
	surfaces["sessions"] = remade
	if replyGoesTo(surfaces, "sessions", old) {
		t.Fatal("a late reply to the surface made before a fleet switch would settle a call of the new one")
	}
	delete(surfaces, "sessions")
	if replyGoesTo(surfaces, "sessions", old) {
		t.Fatal("a reply to a surface taken down is evaluated")
	}
}
