package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/workspace"
)

// twoFleetPanel is a configuration file with two fleets made the way init
// makes them — workspaces with git boards — and a collector over it. A card
// started on either board is committed, so git gets a home of its own: an
// identity, and neither the machine's configuration nor its signing key.
//
//	A: top-level, orchestrator a0000001      B: listed, orchestrator b0000001
func twoFleetPanel(t *testing.T, jobs string) (cfgPath string, cfg config.Config, c *Collector, roots [2]string) {
	t.Helper()
	isolateHome(t)
	dir := t.TempDir()
	for i, name := range []string{"A", "B"} {
		roots[i] = filepath.Join(dir, name)
		if _, err := workspace.Create(roots[i], workspace.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	cfgPath = filepath.Join(dir, "config.yaml")
	base := config.Default()
	base.Name = "A"
	base.BoardPath = workspace.BoardDir(roots[0])
	base.DocsPaths = []string{workspace.DocsDir(roots[0])}
	base.OrchestratorSession = "a0000001"
	if err := config.Save(cfgPath, base); err != nil {
		t.Fatal(err)
	}
	if err := config.AddFleet(cfgPath, fleet.Fleet{Name: "B", BoardPath: workspace.BoardDir(roots[1]), DocsPaths: []string{workspace.DocsDir(roots[1])}, Orchestrator: "b0000001"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	dc := deadDaemon(t)
	if jobs != "" {
		dc = fakeDaemon(t, jobs)
	}
	return cfgPath, cfg, NewCollector(cfg, dc, nil, t.TempDir()), roots
}

func TestPinningAListedFleetsOrchestratorWritesThatFleet(t *testing.T) {
	cfgPath, _, c, _ := twoFleetPanel(t, "")
	if err := pinOrchestrator(cfgPath, c, "B", "cafe0001"); err != nil {
		t.Fatalf("pin B: %v", err)
	}
	onDisk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.FleetList(); got[1].Orchestrator != "cafe0001" || got[0].Orchestrator != "a0000001" {
		t.Fatalf("on disk after pinning B: %+v", got)
	}
	if got := c.Config().FleetList(); got[1].Orchestrator != "cafe0001" {
		t.Fatalf("the running collector did not take the pin: %+v", got)
	}
}

func TestPinningTheFirstFleetStillGoesThroughItsOwnKey(t *testing.T) {
	cfgPath, _, c, _ := twoFleetPanel(t, "")
	if err := pinOrchestrator(cfgPath, c, "A", "cafe0002"); err != nil {
		t.Fatalf("pin A: %v", err)
	}
	onDisk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.OrchestratorSession != "cafe0002" || onDisk.FleetList()[1].Orchestrator != "b0000001" {
		t.Fatalf("on disk after pinning A: %+v", onDisk.FleetList())
	}
}

func TestPinningAnotherFleetsOrchestratorIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	// Through the first fleet's own key too: that write checks only that the
	// file still parses, so without this a panel could write a configuration
	// its next start refuses.
	for _, tc := range []struct{ fleet, id string }{{"B", "a0000001"}, {"A", "b0000001"}} {
		cfgPath, _, c, _ := twoFleetPanel(t, "")
		before, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		err = pinOrchestrator(cfgPath, c, tc.fleet, tc.id)
		if !errors.Is(err, server.ErrOrchestratorTaken) {
			t.Fatalf("pin %s to %s: err = %v, want ErrOrchestratorTaken", tc.fleet, tc.id, err)
		}
		after, _ := os.ReadFile(cfgPath)
		if !bytes.Equal(before, after) {
			t.Fatalf("a refused pin changed the file:\n%s", after)
		}
		if _, err := config.Load(cfgPath); err != nil {
			t.Fatalf("the file no longer loads: %v", err)
		}
	}
}

func TestUnpinningAFleetIsAlwaysAllowed(t *testing.T) {
	cfgPath, _, c, _ := twoFleetPanel(t, "")
	if err := pinOrchestrator(cfgPath, c, "B", ""); err != nil {
		t.Fatalf("unpin B: %v", err)
	}
	if got := c.Config().FleetList()[1].Orchestrator; got != "" {
		t.Fatalf("B still pins %q", got)
	}
}

func TestEachFleetHasItsOwnBoardDocsAndWizard(t *testing.T) {
	cfgPath, cfg, c, roots := twoFleetPanel(t, "")
	resolve := newFleets(runOpts{configPath: cfgPath}, cfg, deadDaemon(t), c)
	b, err := resolve("B")
	if err != nil {
		t.Fatalf("Fleet(B): %v", err)
	}
	if b.BoardDir != workspace.BoardDir(roots[1]) || len(b.DocsRoots) != 1 || b.DocsRoots[0] != workspace.DocsDir(roots[1]) {
		t.Fatalf("B's board %q docs %q", b.BoardDir, b.DocsRoots)
	}
	path, err := b.CreateCard("a task of B", "planned")
	if err != nil {
		t.Fatalf("CreateCard on B: %v", err)
	}
	if !strings.HasPrefix(path, workspace.BoardDir(roots[1])) {
		t.Fatalf("B's card went to %s", path)
	}
	preview, err := b.OrchestratorPreview("en")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Workspace != roots[1] || !strings.HasPrefix(preview.Path, workspace.DocsDir(roots[1])) {
		t.Fatalf("B's wizard would work in %q and write %q", preview.Workspace, preview.Path)
	}
	first, err := resolve("")
	if err != nil || first.BoardDir != workspace.BoardDir(roots[0]) {
		t.Fatalf("Fleet(\"\") = %q, %v, want the first fleet", first.BoardDir, err)
	}
	if _, err := resolve("C"); !errors.Is(err, fleet.ErrUnknown) {
		t.Fatalf("Fleet(C): err = %v, want fleet.ErrUnknown", err)
	}
}

func TestAFleetsWizardPinsThatFleet(t *testing.T) {
	// The last step of an appointment: B's wizard pins B's orchestrator and
	// leaves the first fleet's where it was.
	cfgPath, _, c, _ := twoFleetPanel(t, "")
	b := fleetAppointer(runOpts{configPath: cfgPath}, c.Config().FleetList()[1], deadDaemon(t), c)
	if err := b.Pin("cafe0005"); err != nil {
		t.Fatalf("B's wizard pin: %v", err)
	}
	onDisk, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := onDisk.FleetList(); got[0].Orchestrator != "a0000001" || got[1].Orchestrator != "cafe0005" {
		t.Fatalf("after B's wizard pinned: %+v", got)
	}
}

func TestVetPinLeavesItsArgumentAlone(t *testing.T) {
	_, cfg, _, _ := twoFleetPanel(t, "")
	if err := vetPin(cfg, "B", "cafe0006"); err != nil {
		t.Fatal(err)
	}
	if got := cfg.FleetList()[1].Orchestrator; got != "b0000001" {
		t.Fatalf("vetPin wrote the pin it was asked about into its caller's configuration: %q", got)
	}
}

func TestAFleetsWizardRefusesAnotherFleetsOrchestrator(t *testing.T) {
	cfgPath, cfg, c, roots := twoFleetPanel(t, `{"short":"a0000001"},{"short":"b0000001"}`)
	resolve := newFleets(runOpts{configPath: cfgPath}, cfg, fakeDaemon(t, `{"short":"a0000001"},{"short":"b0000001"}`), c)
	b, err := resolve("B")
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Appoint(context.Background(), orchestrator.Request{Session: "a0000001", Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || len(res.Steps) != 1 || !strings.Contains(res.Steps[0].Error, "already the orchestrator") {
		t.Fatalf("B's wizard appointing A's orchestrator: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(workspace.DocsDir(roots[1]), "orchestrator.md")); err == nil {
		t.Fatal("B's working order was written for a session it will not appoint")
	}
}
