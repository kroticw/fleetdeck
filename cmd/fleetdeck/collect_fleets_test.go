package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/state"
)

// boardWithCards lays out a board whose cards name the given sessions, one
// card each, and returns the board's path.
func boardWithCards(t *testing.T, sessions ...string) string {
	t.Helper()
	dir := t.TempDir()
	cards := filepath.Join(dir, "cards")
	if err := os.MkdirAll(cards, 0o700); err != nil {
		t.Fatal(err)
	}
	for i, s := range sessions {
		body := fmt.Sprintf("---\nzone: planned\nstage: active\nprogress: 20\nsession: %s\nrepo: x/y\ncreated: 2026-09-11\n---\n\n# card %d\n", s, i)
		if err := os.WriteFile(filepath.Join(cards, fmt.Sprintf("c%d.md", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// twoFleetConfig is the acceptance shape: two fleets with their own boards
// and their own orchestrators.
//
//	a0000001  A's orchestrator    b0000001  B's orchestrator
//	a0000002  on A's board         b0000002  on B's board
//	n0000001  on no board
func twoFleetConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.Name = "A"
	cfg.BoardPath = boardWithCards(t, "a0000002")
	cfg.OrchestratorSession = "a0000001"
	cfg.Fleets = []fleet.Fleet{{Name: "B", BoardPath: boardWithCards(t, "b0000002"), Orchestrator: "b0000001"}}
	return cfg
}

const twoFleetJobs = `{"short":"a0000001"},{"short":"a0000002"},{"short":"b0000001"},{"short":"b0000002"},{"short":"n0000001"}`

func TestCollectReadsEveryFleetsBoard(t *testing.T) {
	cfg := twoFleetConfig(t)
	whole := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if len(whole.Boards) != 2 {
		t.Fatalf("want a board per fleet, got %d", len(whole.Boards))
	}
	for i, want := range []string{"a0000002", "b0000002"} {
		b := whole.Boards[i]
		if b.BoardError != "" || len(b.Cards) != 1 || b.Cards[0].Session != want {
			t.Fatalf("board %d (%s): error %q, cards %+v", i, b.Fleet.Name, b.BoardError, b.Cards)
		}
	}
	// Every card of every board is diffed for banners, whichever fleet a tab shows.
	if len(whole.Cards) != 2 {
		t.Fatalf("the whole snapshot must carry every board's cards, got %d", len(whole.Cards))
	}
}

func TestAFleetsBrokenBoardIsThatFleetsError(t *testing.T) {
	cfg := twoFleetConfig(t)
	cfg.Fleets[0].BoardPath = t.TempDir() // no cards directory
	whole := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if whole.BoardError != "" {
		t.Fatalf("B's broken board leaked into the first fleet's error: %q", whole.BoardError)
	}
	if whole.Boards[1].BoardError == "" {
		t.Fatal("B's broken board must be reported as B's")
	}
	if len(whole.Boards[0].Cards) != 1 {
		t.Fatal("A's board must survive B's")
	}
}

func TestEachFleetGetsItsOwnSessionsCardsAndOrchestrator(t *testing.T) {
	// The acceptance, measured on a collector over a daemon round trip: two
	// fleets, their own boards, their own sessions; neither view mixes up
	// cards, sessions or the orchestrator.
	cfg := twoFleetConfig(t)
	whole := NewCollector(cfg, fakeDaemon(t, twoFleetJobs), nil, t.TempDir()).Collect(context.Background())
	if whole.DaemonError != "" {
		t.Fatalf("fake daemon: %s", whole.DaemonError)
	}

	for _, tc := range []struct {
		fleet, orchestrator, card string
		own                       []string
	}{
		{"A", "a0000001", "a0000002", []string{"a0000001", "a0000002"}},
		{"B", "b0000001", "b0000002", []string{"b0000001", "b0000002"}},
	} {
		view, err := state.ForFleet(whole, tc.fleet)
		if err != nil {
			t.Fatalf("ForFleet(%s): %v", tc.fleet, err)
		}
		if view.OrchestratorSession != tc.orchestrator {
			t.Errorf("%s pins %q, want %q", tc.fleet, view.OrchestratorSession, tc.orchestrator)
		}
		if len(view.Cards) != 1 || view.Cards[0].Session != tc.card {
			t.Errorf("%s shows cards %+v", tc.fleet, view.Cards)
		}
		var own, unclaimed []string
		for _, s := range view.Sessions {
			switch {
			case reflect.DeepEqual(s.Fleets, []string{tc.fleet}):
				own = append(own, s.Short)
			case len(s.Fleets) == 0:
				unclaimed = append(unclaimed, s.Short)
			}
			if s.CardPath != "" && !strings.HasPrefix(s.CardPath, filepath.Dir(view.Cards[0].Path)) {
				t.Errorf("%s links %s to another fleet's card %s", tc.fleet, s.Short, s.CardPath)
			}
		}
		if !reflect.DeepEqual(own, tc.own) {
			t.Errorf("%s's own sessions = %v, want %v", tc.fleet, own, tc.own)
		}
		if !reflect.DeepEqual(unclaimed, []string{"n0000001"}) {
			t.Errorf("%s's unclaimed sessions = %v, want [n0000001]", tc.fleet, unclaimed)
		}
		if len(view.Sessions) != 5 {
			t.Errorf("%s's view dropped sessions: %d of 5", tc.fleet, len(view.Sessions))
		}
	}
}

func TestSetFleetOrchestratorChangesTheNextCollect(t *testing.T) {
	cfg := twoFleetConfig(t)
	c := NewCollector(cfg, deadDaemon(t), nil, t.TempDir())
	if !c.SetFleetOrchestrator("B", "cafe0001") {
		t.Fatal("SetFleetOrchestrator(B) found no fleet B")
	}
	whole := c.Collect(context.Background())
	if got := whole.Boards[1].Fleet.Orchestrator; got != "cafe0001" {
		t.Fatalf("B's orchestrator after the pin = %q", got)
	}
	if whole.OrchestratorSession != "a0000001" {
		t.Fatalf("pinning B moved the first fleet's orchestrator to %q", whole.OrchestratorSession)
	}
	if c.SetFleetOrchestrator("C", "cafe0002") {
		t.Fatal("SetFleetOrchestrator(C) claimed a fleet that is not configured")
	}
}

func TestConfigReturnsAnIndependentCopyOfFleets(t *testing.T) {
	c := NewCollector(twoFleetConfig(t), nil, nil, t.TempDir())
	cfg := c.Config()
	c.SetFleetOrchestrator("B", "cafe0003")
	if cfg.Fleets[0].Orchestrator != "b0000001" {
		t.Fatalf("a Config() taken before the pin changed under its holder: %q", cfg.Fleets[0].Orchestrator)
	}
}
