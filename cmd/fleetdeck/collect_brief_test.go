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
	if snap.OrchestratorBriefForeign {
		t.Error("the wizard's own brief is called one fleetdeck did not write")
	}
}

// A file at the brief's path that fleetdeck did not write is quieter than no
// file at all. An orchestrator notices an absent brief the moment it goes to
// read it; a foreign one it reads, and works by. The wizard already refuses
// to replace such a file (orchestrator.ErrNotOurs), but the column's dropdown
// pins without asking the disk anything, so the panel has to say it.
//
// These put a file without the marker where the brief belongs and look at
// what the snapshot says -- checked on disk rather than argued.

// foreignDocsDir is a documentation directory holding an orchestrator.md that
// a person wrote.
func foreignDocsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orchestrator.md"), []byte("# my own notes about orchestrating\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCollectReportsAPinnedOrchestratorBesideAForeignBrief(t *testing.T) {
	docs := foreignDocsDir(t)
	cfg := briefConfig(t, docs, "06a1f607")

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if !snap.OrchestratorBriefForeign {
		t.Error("a session is pinned beside a file fleetdeck did not write, and the snapshot does not say so — the session will read it as its working order")
	}
	if snap.OrchestratorBriefMissing {
		t.Error("the file is on disk; calling it missing sends the operator to a wizard that will refuse to overwrite it")
	}
	if want := filepath.Join(docs, "orchestrator.md"); snap.OrchestratorBriefPath != want {
		t.Errorf("brief path = %q, want %q", snap.OrchestratorBriefPath, want)
	}
}

// Nothing pinned claims nothing, the same as for a missing brief: a person's
// own orchestrator.md in a docs directory is their business until a session
// is said to work by it.
func TestCollectSaysNothingOfAForeignBriefWhenNothingIsPinned(t *testing.T) {
	cfg := briefConfig(t, foreignDocsDir(t), "")

	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if snap.OrchestratorBriefForeign || snap.OrchestratorBriefMissing {
		t.Errorf("foreign %v, missing %v; nothing is pinned, so neither", snap.OrchestratorBriefForeign, snap.OrchestratorBriefMissing)
	}
}

// The way out the wizard itself names -- move the file aside, run the wizard --
// clears the report on the very next cycle. A report that outlived the fix
// would be as wrong as the silence it replaces.
func TestCollectStopsReportingAForeignBriefOnceTheWizardHasWrittenItsOwn(t *testing.T) {
	docs := foreignDocsDir(t)
	cfg := briefConfig(t, docs, "06a1f607")
	collector := NewCollector(cfg, deadDaemon(t), nil, t.TempDir())

	if !collector.Collect(context.Background()).OrchestratorBriefForeign {
		t.Fatal("the foreign brief was never reported to begin with")
	}

	path := filepath.Join(docs, "orchestrator.md")
	if err := os.Rename(path, path+".mine"); err != nil {
		t.Fatal(err)
	}
	paths := orchestrator.Paths{Board: cfg.BoardPath, Docs: cfg.DocsPaths}
	brief, err := orchestrator.Brief("en", paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.WriteBrief(path, brief); err != nil {
		t.Fatal(err)
	}

	snap := collector.Collect(context.Background())
	if snap.OrchestratorBriefForeign || snap.OrchestratorBriefMissing {
		t.Errorf("foreign %v, missing %v after the wizard wrote its own brief; want neither", snap.OrchestratorBriefForeign, snap.OrchestratorBriefMissing)
	}
}

// Each fleet is asked about its own file: a person's orchestrator.md in one
// fleet's docs says nothing about another fleet whose brief is the wizard's.
func TestCollectAsksEveryFleetWhetherItsOwnBriefIsOurs(t *testing.T) {
	firstDocs := docsDir(t, false)
	firstBoard := boardWithCards(t)
	paths := orchestrator.Paths{Board: firstBoard, Docs: []string{firstDocs}}
	brief, err := orchestrator.Brief("en", paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.WriteBrief(orchestrator.BriefPath(paths), brief); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UsageEnabled = false
	cfg.Name = "A"
	cfg.BoardPath = firstBoard
	cfg.DocsPaths = []string{firstDocs}
	cfg.OrchestratorSession = "a0000001"
	cfg.Fleets = []fleet.Fleet{{
		Name:         "B",
		BoardPath:    boardWithCards(t),
		DocsPaths:    []string{foreignDocsDir(t)},
		Orchestrator: "b0000001",
	}}

	whole := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if len(whole.Boards) != 2 {
		t.Fatalf("want a board per fleet, got %d", len(whole.Boards))
	}
	if whole.Boards[0].BriefForeign || whole.OrchestratorBriefForeign {
		t.Error("fleet A's brief is the wizard's own")
	}
	if !whole.Boards[1].BriefForeign {
		t.Error("fleet B is pinned beside a person's file, and its own board entry does not say so")
	}
	view, err := state.ForFleet(whole, "B")
	if err != nil {
		t.Fatal(err)
	}
	if !view.OrchestratorBriefForeign {
		t.Error("B's own view does not carry B's foreign brief")
	}
}
