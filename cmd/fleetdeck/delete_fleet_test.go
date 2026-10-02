package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/state"
)

// deleteStand is a panel serving two fleets: main, the first, and vpn, listed,
// each with its workspace under root. vpn's docs live where docsAt says.
type deleteStand struct {
	root, cfgPath string
	primary, vpn  fleet.Fleet
	live          *liveFleets
	collector     *Collector
	watchEnded    chan struct{}
	snap          state.Snapshot
	stopped       []string
	refreshes     atomic.Int32
}

func newDeleteStand(t *testing.T, docsAt func(root string) string, primaryDocs ...string) *deleteStand {
	t.Helper()
	root := t.TempDir()
	s := &deleteStand{root: root, cfgPath: filepath.Join(root, "config.yaml"), watchEnded: make(chan struct{})}
	s.primary = fleet.Fleet{Name: "main", BoardPath: filepath.Join(root, "main", "board")}
	for _, d := range primaryDocs {
		s.primary.DocsPaths = append(s.primary.DocsPaths, filepath.Join(root, d))
	}
	s.vpn = fleet.Fleet{Name: "vpn", BoardPath: filepath.Join(root, "vpn", "board"), DocsPaths: []string{docsAt(root)}}
	for _, p := range []string{s.primary.BoardPath, s.vpn.BoardPath, s.vpn.DocsPaths[0]} {
		if err := os.MkdirAll(filepath.Join(p, "inside"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Name = s.primary.Name
	cfg.BoardPath = s.primary.BoardPath
	cfg.DocsPaths = s.primary.DocsPaths
	cfg.Fleets = []fleet.Fleet{s.vpn}
	if err := config.Save(s.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	s.collector = NewCollector(cfg, nil, nil, "")
	s.live = &liveFleets{
		ctx:       t.Context(),
		collector: s.collector,
		watch: func(ctx context.Context, dir string, _ func()) {
			<-ctx.Done()
			if dir == s.vpn.BoardPath {
				close(s.watchEnded)
			}
		},
		refresh: func(context.Context) {},
	}
	s.live.watchBoard(s.vpn)
	s.snap = state.Snapshot{
		Sessions: []state.SessionView{
			{Session: daemon.Session{Short: "exclusive"}},
			{Session: daemon.Session{Short: "shared"}},
			{Session: daemon.Session{Short: "stopped"}, Lifecycle: state.LifecycleStopped},
		},
		Boards: []state.FleetBoard{
			{Fleet: s.primary, Cards: []board.Card{{Session: "shared"}}},
			{Fleet: s.vpn, Cards: []board.Card{{Session: "exclusive"}, {Session: "shared"}, {Session: "stopped"}}},
		},
	}
	return s
}

func (s *deleteStand) deleter(stop func(context.Context, string) error, home string) func(string) ([]string, error) {
	return fleetDeleter(s.cfgPath, s.live, func() state.Snapshot { return s.snap }, func(context.Context) { s.refreshes.Add(1) }, stop, home)
}

func (s *deleteStand) recordStop(_ context.Context, short string) error {
	s.stopped = append(s.stopped, short)
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The fleet's own sessions are stopped, a session another fleet also claims is
// not, the entry leaves the file and the running panel, its watch ends, and
// its workspace is removed. Nothing of the first fleet is touched.
func TestDeletingAFleetStopsItsOwnSessionsAndRemovesItsWorkspace(t *testing.T) {
	s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "vpn", "docs") })

	kept, err := s.deleter(s.recordStop, "/nonexistent-home")("vpn")
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Errorf("kept = %v, want nothing kept", kept)
	}
	if !slices.Equal(s.stopped, []string{"exclusive"}) {
		t.Errorf("stopped = %v, want [exclusive]", s.stopped)
	}
	select {
	case <-s.watchEnded:
	case <-time.After(time.Second):
		t.Fatal("the deleted fleet's board is still watched")
	}
	if exists(filepath.Join(s.root, "vpn")) {
		t.Error("the deleted fleet's workspace is still there")
	}
	if !exists(s.primary.BoardPath) {
		t.Error("the first fleet's board was removed")
	}
	got, err := config.Load(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Fleets) != 0 || len(s.collector.Config().Fleets) != 0 {
		t.Errorf("the fleet is still configured: file %v, panel %v", got.Fleets, s.collector.Config().Fleets)
	}
	if s.refreshes.Load() != 1 {
		t.Errorf("refreshes = %d, want 1", s.refreshes.Load())
	}
}

// Documentation configured outside the fleet's workspace is somebody's
// project, not the fleet's: it stays on disk and the answer names it.
func TestDocumentationOutsideTheWorkspaceIsKept(t *testing.T) {
	s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "project", "docs") })

	kept, err := s.deleter(s.recordStop, "/nonexistent-home")("vpn")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kept, s.vpn.DocsPaths) {
		t.Errorf("kept = %v, want %v", kept, s.vpn.DocsPaths)
	}
	if !exists(filepath.Join(s.vpn.DocsPaths[0], "inside")) {
		t.Error("documentation outside the workspace was removed")
	}
	if exists(s.vpn.BoardPath) {
		t.Error("the board was not removed")
	}
}

// refusedUntouched asserts a deletion was refused and changed nothing: no
// session stopped, the fleet still configured and its board still on disk.
func (s *deleteStand) refusedUntouched(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("the deletion was not refused")
	}
	if len(s.stopped) != 0 {
		t.Errorf("a refused deletion stopped %v", s.stopped)
	}
	got, loadErr := config.Load(s.cfgPath)
	if loadErr != nil || len(got.Fleets) != 1 {
		t.Errorf("a refused deletion changed the configuration: %v %v", got.Fleets, loadErr)
	}
	if !exists(filepath.Join(s.vpn.BoardPath, "inside")) {
		t.Error("a refused deletion removed the board")
	}
}

func TestTheFirstFleetCannotBeDeleted(t *testing.T) {
	s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "vpn", "docs") })
	_, err := s.deleter(s.recordStop, "/nonexistent-home")("main")
	s.refusedUntouched(t, err)
	if !exists(s.primary.BoardPath) {
		t.Error("the first fleet's board was removed")
	}
}

// Another fleet reading documentation inside this fleet's workspace would lose
// it, so the deletion is refused rather than half done.
func TestAFleetSharingADirectoryWithAnotherCannotBeDeleted(t *testing.T) {
	s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "vpn", "docs") }, filepath.Join("vpn", "docs"))
	_, err := s.deleter(s.recordStop, "/nonexistent-home")("vpn")
	s.refusedUntouched(t, err)
}

// Documentation of this fleet that points at another fleet's board lies
// outside this workspace, so it is kept and the other fleet loses nothing.
func TestDocumentationPointingAtAnotherFleetIsKept(t *testing.T) {
	s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "main", "board") })
	kept, err := s.deleter(s.recordStop, "/nonexistent-home")("vpn")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(kept, s.vpn.DocsPaths) || !exists(filepath.Join(s.primary.BoardPath, "inside")) {
		t.Errorf("kept %v; the other fleet's board must stay whole", kept)
	}
}

// A board kept straight in the home directory makes the home directory the
// workspace; removing that is never what deleting a fleet means.
func TestAWorkspaceThatIsTheHomeDirectoryOrAboveItIsRefused(t *testing.T) {
	for _, home := range []string{"vpn", filepath.Join("vpn", "below")} {
		s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "vpn", "docs") })
		_, err := s.deleter(s.recordStop, filepath.Join(s.root, home))("vpn")
		s.refusedUntouched(t, err)
	}
}

// A panel that cannot stop sessions (a stand given no claude) would leave the
// fleet's sessions running against a board that is gone, so it does nothing.
func TestAPanelThatCannotStopSessionsDeletesNothing(t *testing.T) {
	s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "vpn", "docs") })
	_, err := s.deleter(nil, "/nonexistent-home")("vpn")
	s.refusedUntouched(t, err)
}

// A stop that fails ends the deletion before the configuration or the disk is
// touched: the operator can try again once the session is dealt with.
func TestAFailedStopDeletesNothing(t *testing.T) {
	s := newDeleteStand(t, func(root string) string { return filepath.Join(root, "vpn", "docs") })
	_, err := s.deleter(func(context.Context, string) error { return errors.New("no") }, "/nonexistent-home")("vpn")
	if err == nil {
		t.Fatal("a failed stop was not reported")
	}
	got, _ := config.Load(s.cfgPath)
	if len(got.Fleets) != 1 || !exists(s.vpn.BoardPath) {
		t.Error("a failed stop still removed the fleet")
	}
}
