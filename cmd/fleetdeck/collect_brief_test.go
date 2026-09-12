package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/state"
)

// The pin means one thing: this session has been given the fleet's working
// order. It can be set by a path that writes no working order at all -- the
// orchestrator column's dropdown moves the pin and sends nothing -- and the
// file can also be deleted after a real appointment wrote it. Both leave the
// configuration saying an orchestrator is appointed while the thing the
// appointment exists to produce is not there, and nothing used to say so.
//
// So every cycle asks the disk, and these tests pin the answer for each state
// the pair of fields can legitimately be in.

// docsDir is a documentation directory, optionally with a brief already in it.
func docsDir(t *testing.T, withBrief bool) string {
	t.Helper()
	dir := t.TempDir()
	if withBrief {
		if err := os.WriteFile(filepath.Join(dir, "orchestrator.md"), []byte("# working order\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// briefConfig is one fleet with a board, a documentation directory and
// whichever session is pinned.
func briefConfig(t *testing.T, docs, pinned string) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.BoardPath = boardWithCards(t)
	cfg.DocsPaths = []string{docs}
	cfg.OrchestratorSession = pinned
	return cfg
}

func TestCollectReportsAPinnedOrchestratorWithNoBriefOnDisk(t *testing.T) {
	docs := docsDir(t, false)
	cfg := briefConfig(t, docs, "06a1f607")

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	want := filepath.Join(docs, "orchestrator.md")
	if snap.OrchestratorBriefPath != want {
		t.Errorf("brief path = %q, want %q", snap.OrchestratorBriefPath, want)
	}
	if !snap.OrchestratorBriefMissing {
		t.Error("a session is pinned and no brief is on disk, and the snapshot does not say so — this is the silent failure itself")
	}
}

func TestCollectReportsNothingWrongWhenTheBriefIsThere(t *testing.T) {
	docs := docsDir(t, true)
	cfg := briefConfig(t, docs, "06a1f607")

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.OrchestratorBriefMissing {
		t.Error("the brief is on disk and the snapshot calls it missing")
	}
	if want := filepath.Join(docs, "orchestrator.md"); snap.OrchestratorBriefPath != want {
		t.Errorf("brief path = %q, want %q", snap.OrchestratorBriefPath, want)
	}
}

// Nothing pinned is not a failure: an absent brief claims nothing when no
// session is claimed to have been given one. A warning here would fire on
// every fresh panel and teach the operator to ignore the row.
func TestCollectSaysNothingIsMissingWhenNothingIsPinned(t *testing.T) {
	cfg := briefConfig(t, docsDir(t, false), "")

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.OrchestratorBriefMissing {
		t.Error("nothing is pinned, so nothing is missing")
	}
	if snap.OrchestratorBriefPath == "" {
		t.Error("the path is where a brief would go and is known whether or not one is pinned")
	}
}

// A directory of that name is not a working order any orchestrator can read.
// Calling it present would be the same silence in a new place.
func TestCollectCountsSomethingThatIsNotAFileAsNoBrief(t *testing.T) {
	docs := docsDir(t, false)
	if err := os.Mkdir(filepath.Join(docs, "orchestrator.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := briefConfig(t, docs, "06a1f607")

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if !snap.OrchestratorBriefMissing {
		t.Error("a directory named orchestrator.md is not a brief")
	}
}

// The brief goes in the first documentation directory, and in the board when
// the fleet has none -- BriefPath's own rule, which this reports rather than
// restates.
func TestCollectPutsTheBriefInTheBoardWhenThereIsNoDocsDirectory(t *testing.T) {
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.BoardPath = boardWithCards(t)
	cfg.OrchestratorSession = "06a1f607"

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if want := filepath.Join(cfg.BoardPath, "orchestrator.md"); snap.OrchestratorBriefPath != want {
		t.Errorf("brief path = %q, want %q", snap.OrchestratorBriefPath, want)
	}
	if !snap.OrchestratorBriefMissing {
		t.Error("nothing was written to the board, so the brief is missing")
	}
}

// A panel with neither a board nor a documentation directory has nowhere to
// put a brief, and an appointment there is refused outright
// (orchestrator.ErrNoBoard). There is no claim to contradict, so there is no
// path to report and nothing to call missing.
func TestCollectReportsNoBriefPathWithNowhereToPutOne(t *testing.T) {
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.OrchestratorSession = "06a1f607"

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.OrchestratorBriefPath != "" || snap.OrchestratorBriefMissing {
		t.Errorf("path %q, missing %v; want neither", snap.OrchestratorBriefPath, snap.OrchestratorBriefMissing)
	}
}

// Each fleet is asked about its own brief, in its own documentation
// directory, about its own orchestrator. A fleet whose brief is there must
// not be tarred by another fleet's missing one, and the top-level fields stay
// the first fleet's -- the same rule every other per-fleet field follows.
func TestCollectAsksEveryFleetAboutItsOwnBrief(t *testing.T) {
	firstDocs := docsDir(t, true)
	secondDocs := docsDir(t, false)
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.Name = "A"
	cfg.BoardPath = boardWithCards(t)
	cfg.DocsPaths = []string{firstDocs}
	cfg.OrchestratorSession = "a0000001"
	cfg.Fleets = []fleet.Fleet{{
		Name:         "B",
		BoardPath:    boardWithCards(t),
		DocsPaths:    []string{secondDocs},
		Orchestrator: "b0000001",
	}}

	whole := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if len(whole.Boards) != 2 {
		t.Fatalf("want a board per fleet, got %d", len(whole.Boards))
	}
	if whole.Boards[0].BriefMissing {
		t.Error("fleet A's brief is on disk")
	}
	if !whole.Boards[1].BriefMissing {
		t.Error("fleet B is pinned with no brief, and its own board entry does not say so")
	}
	if whole.OrchestratorBriefMissing {
		t.Error("the top-level fields are the first fleet's, and its brief is there")
	}

	view, err := state.ForFleet(whole, "B")
	if err != nil {
		t.Fatal(err)
	}
	if !view.OrchestratorBriefMissing {
		t.Error("B's own view does not carry B's missing brief")
	}
	if want := filepath.Join(secondDocs, "orchestrator.md"); view.OrchestratorBriefPath != want {
		t.Errorf("B's brief path = %q, want %q", view.OrchestratorBriefPath, want)
	}
}

// The brief a real appointment writes is reported as present. This is the one
// test that goes through orchestrator.WriteBrief rather than writing a file
// of its own: a check that only recognized files this test wrote would pass
// while missing every brief the wizard produces.
func TestCollectRecognizesABriefTheWizardWouldWrite(t *testing.T) {
	docs := docsDir(t, false)
	cfg := briefConfig(t, docs, "06a1f607")

	paths := orchestrator.Paths{Board: cfg.BoardPath, Docs: cfg.DocsPaths}
	brief, err := orchestrator.Brief("ru", paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.WriteBrief(orchestrator.BriefPath(paths), brief); err != nil {
		t.Fatal(err)
	}

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.OrchestratorBriefMissing {
		t.Error("the wizard's own brief is not recognized as one")
	}
}
