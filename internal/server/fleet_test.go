package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/state"
)

// twoFleetDeps serves a whole snapshot of two fleets, the way the panel's
// collect cycle produces one.
func twoFleetDeps() Deps {
	return Deps{Snapshot: func() state.Snapshot {
		a := []board.Card{{Path: "/a/board/cards/one.md", Session: "a0000002"}}
		b := []board.Card{{Path: "/b/board/cards/two.md", Session: "b0000002"}}
		return state.Snapshot{
			Cards:               append(append([]board.Card{}, a...), b...),
			OrchestratorSession: "a0000001",
			Boards: []state.FleetBoard{
				{Fleet: fleet.Fleet{Name: "A", BoardPath: "/a/board", Orchestrator: "a0000001"}, Cards: a},
				{Fleet: fleet.Fleet{Name: "B", BoardPath: "/b/board", Orchestrator: "b0000001"}, Cards: b},
			},
		}
	}}
}

func decodeSnapshot(t *testing.T, body string) state.Snapshot {
	t.Helper()
	var snap state.Snapshot
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("not a snapshot: %v (%s)", err, body)
	}
	return snap
}

func TestSnapshotRouteServesTheFleetTheTabNames(t *testing.T) {
	rec := do(twoFleetDeps(), http.MethodGet, "/api/snapshot?fleet=B", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	snap := decodeSnapshot(t, rec.Body.String())
	if snap.Fleet != "B" || snap.OrchestratorSession != "b0000001" || len(snap.Cards) != 1 || snap.Cards[0].Session != "b0000002" {
		t.Fatalf("fleet B's tab got %+v", snap)
	}
}

func TestSnapshotRouteWithNoFleetServesTheFirst(t *testing.T) {
	rec := do(twoFleetDeps(), http.MethodGet, "/api/snapshot", "")
	snap := decodeSnapshot(t, rec.Body.String())
	if snap.Fleet != "A" || len(snap.Cards) != 1 || snap.Cards[0].Session != "a0000002" {
		t.Fatalf("a tab naming no fleet got %+v", snap)
	}
}

func TestSnapshotRouteRefusesAnUnknownFleet(t *testing.T) {
	rec := do(twoFleetDeps(), http.MethodGet, "/api/snapshot?fleet=C", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown fleet answered %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `\"A\", \"B\"`) {
		t.Fatalf("the refusal does not name the fleets there are: %s", rec.Body)
	}
}

func TestWebSocketPushesTheFleetTheTabNames(t *testing.T) {
	d := twoFleetDeps()
	d.interval = 10 * time.Second
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws?fleet=B", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	snap := readSnapshot(ctx, t, conn)
	if snap.Fleet != "B" || snap.OrchestratorSession != "b0000001" {
		t.Fatalf("fleet B's socket pushed fleet %q, orchestrator %q", snap.Fleet, snap.OrchestratorSession)
	}
}

func TestWebSocketClosesRatherThanPushAnotherFleet(t *testing.T) {
	// Fleets do not change under a running panel today; if that ever stops
	// holding, a socket opened for B must close, not start pushing A.
	var calls atomic.Int32
	two := twoFleetDeps().Snapshot
	d := Deps{Snapshot: func() state.Snapshot {
		snap := two()
		if calls.Add(1) > 2 { // the upgrade check and the first push see B
			snap.Boards = snap.Boards[:1]
		}
		return snap
	}}
	d.interval = 20 * time.Millisecond
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws?fleet=B", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	if snap := readSnapshot(ctx, t, conn); snap.Fleet != "B" {
		t.Fatalf("first push was fleet %q", snap.Fleet)
	}
	_, data, err := conn.Read(ctx)
	if err == nil {
		t.Fatalf("the socket pushed %s after its fleet was gone", data)
	}
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("want a policy-violation close, got %v", err)
	}
}

func TestWebSocketRefusesAnUnknownFleetBeforeUpgrading(t *testing.T) {
	url, _ := wsServer(t, twoFleetDeps())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url+"/ws?fleet=C", nil)
	if err == nil {
		conn.CloseNow()
		t.Fatal("a socket for an unknown fleet was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 before the upgrade, got %v (%v)", resp, err)
	}
}
