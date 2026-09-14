package supervisor

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// collectWindow runs k's window route into a slice until want events have come
// or a second has passed, then waits a moment more for any duplicate.
func collectWindow(t *testing.T, k *KeeperEvents, want int) []Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []Event
	go k.Run(ctx, func(e Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= want {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return append([]Event(nil), got...)
}

func TestTheKeepersWordBeforeTheWindowIsReadyReachesItAllInOrderOnce(t *testing.T) {
	k := NewKeeperEvents()
	said := []Event{
		{State: Starting, PID: 1},
		{State: Answering, Ours: true, PID: 1, Took: time.Second},
		{State: Starting, PID: 2},
		{State: Failed, Err: errors.New("the panel ended")},
	}
	for _, e := range said {
		k.Push(e)
	}
	got := collectWindow(t, k, len(said))
	if len(got) != len(said) {
		t.Fatalf("the window heard %d events, want the keeper's %d: %+v", len(got), len(said), got)
	}
	for i := range said {
		if got[i].State != said[i].State || got[i].PID != said[i].PID {
			t.Fatalf("event %d is %+v, want %+v: the order the keeper said them in", i, got[i], said[i])
		}
	}
}

func TestTheKeepersWordAfterTheWindowIsReadyIsHeardToo(t *testing.T) {
	k := NewKeeperEvents()
	k.Push(Event{State: Starting, PID: 7})
	go func() {
		time.Sleep(30 * time.Millisecond)
		k.Push(Event{State: Answering, Ours: true, PID: 7})
	}()
	got := collectWindow(t, k, 2)
	if len(got) != 2 || got[0].State != Starting || got[1].State != Answering {
		t.Fatalf("heard %+v, want starting then answering", got)
	}
}

// A takeover reads its events only while it waits on its panel, and a keeper
// may say many things between two waits. None of them may be dropped: the one
// dropped could be the answer the takeover is waiting for.
func TestATakeoverThatHasNotReadYetLosesNoEvent(t *testing.T) {
	k := NewKeeperEvents()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, stop := k.route(ctx)
	defer stop()
	const said = 100
	for i := 1; i <= said; i++ {
		k.Push(Event{State: Starting, PID: i})
	}
	for i := 1; i <= said; i++ {
		select {
		case e := <-events:
			if e.PID != i {
				t.Fatalf("event %d the takeover heard has pid %d: an event before it was lost or reordered", i, e.PID)
			}
		case <-time.After(time.Second):
			t.Fatalf("the takeover heard %d events of the %d pushed", i-1, said)
		}
	}
}

// The takeover hears its panel whether or not the window has started reading:
// the window's route and the takeover's are separate.
func TestATakeoverHearsTheKeeperWhileTheWindowIsNotReading(t *testing.T) {
	k := NewKeeperEvents()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, stop := k.route(ctx)
	defer stop()
	k.Push(Event{State: Answering, Ours: true, PID: 5})
	select {
	case e := <-events:
		if e.PID != 5 {
			t.Fatalf("the takeover heard %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("the takeover did not hear the answer while the window was not reading")
	}
}

// A takeover that returns without reading -- failed, or out of time -- must not
// leave the route's goroutine blocked on a send nobody will receive.
func TestARouteLeftUnreadDoesNotOutliveItsTakeover(t *testing.T) {
	before := runtime.NumGoroutine()
	k := NewKeeperEvents()
	_, stop := k.route(context.Background())
	for i := 0; i < 10; i++ {
		k.Push(Event{State: Starting, PID: i})
	}
	stop()
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("%d goroutines a second after the route ended, %d before it began: its feeder is still blocked", n, before)
	}
}

func TestAnEndedTakeoverHearsNothingMore(t *testing.T) {
	k := NewKeeperEvents()
	events, stop := k.route(context.Background())
	stop()
	k.Push(Event{State: Starting, PID: 9})
	select {
	case e := <-events:
		t.Fatalf("an ended takeover heard %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}
