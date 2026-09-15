package main

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
)

// A fleet made while the panel is stopping is refused rather than served by a
// panel that is going away: nothing is added and no watch is started that the
// shutdown would not wait for.
func TestAFleetMadeWhileThePanelStopsIsNotServed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	collector := NewCollector(config.Config{BoardPath: "/fleets/first/board"}, nil, nil, "")
	var watches, refreshes atomic.Int32
	live := &liveFleets{
		ctx:       ctx,
		collector: collector,
		watch:     func(context.Context, string, func()) { watches.Add(1) },
		refresh:   func(context.Context) { refreshes.Add(1) },
	}
	cancel()

	if err := live.add(fleet.Fleet{Name: "vpn", BoardPath: "/fleets/vpn/board"}); err == nil {
		t.Fatal("a stopping panel took a new fleet")
	}
	live.wait()
	if n := watches.Load(); n != 0 {
		t.Fatalf("a stopping panel started %d watches", n)
	}
	if got := collector.Config().Fleets; len(got) != 0 {
		t.Fatalf("a stopping panel added fleets: %+v", got)
	}
}

// The control: the same panel running takes the fleet, watches its board once
// and refreshes, and a second add of it does neither again.
func TestARunningPanelTakesAFleetOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collector := NewCollector(config.Config{BoardPath: "/fleets/first/board"}, nil, nil, "")
	var watches, refreshes atomic.Int32
	live := &liveFleets{
		ctx:       ctx,
		collector: collector,
		watch:     func(context.Context, string, func()) { watches.Add(1) },
		refresh:   func(context.Context) { refreshes.Add(1) },
	}
	vpn := fleet.Fleet{Name: "vpn", BoardPath: "/fleets/vpn/board"}
	for range 2 {
		if err := live.add(vpn); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	live.wait()
	if n := watches.Load(); n != 1 {
		t.Fatalf("%d watches of one fleet made twice, want 1", n)
	}
	if n := refreshes.Load(); n != 1 {
		t.Fatalf("%d refreshes for one new fleet, want 1", n)
	}
	if got := collector.Config().Fleets; len(got) != 1 || got[0].Name != "vpn" {
		t.Fatalf("the collector's fleets: %+v", got)
	}

	// A fleet whose name another fleet has, on a board of its own, is refused.
	if err := (&liveFleets{ctx: context.Background(), collector: collector, watch: live.watch, refresh: live.refresh}).add(fleet.Fleet{Name: "vpn", BoardPath: "/elsewhere/board"}); err == nil {
		t.Fatal("a second fleet named vpn on another board was taken")
	}
}
