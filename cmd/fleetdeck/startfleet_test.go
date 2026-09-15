package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/workspace"
)

// watchCount counts the board watches a panel starts, by board, and runs each
// one for real: a test that only counted would not see a watch that never
// wakes the panel.
type watchCount struct {
	mu sync.Mutex
	n  map[string]int
}

func (w *watchCount) watch(ctx context.Context, boardDir string, onChange func()) {
	w.mu.Lock()
	w.n[filepath.Clean(boardDir)]++
	w.mu.Unlock()
	watchBoard(ctx, boardDir, onChange)
}

func (w *watchCount) of(boardDir string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n[filepath.Clean(boardDir)]
}

// startPanel is a panel on one fleet, the way the start page finds it: the
// first fleet is "obsidian", its board at ~/obsidian/board.
//
// The panel polls once an hour, so within a test only a board watch — or the
// panel's own refresh — brings a card edit into the snapshot.
type startPanel struct {
	home, cfgPath, firstBoard string
	c                         panelClient
	watches                   *watchCount
}

func panelOnOneFleetWatched(t *testing.T) startPanel {
	t.Helper()
	home := isolateHome(t)
	firstBoard := filepath.Join(home, "obsidian", "board")
	if err := os.MkdirAll(filepath.Join(firstBoard, "cards"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.BoardPath = firstBoard
	cfg.UsageEnabled = false
	cfg.ServerPort = freePort(t)
	cfg.DaemonPollInterval = time.Hour
	// Not config.DefaultPath(): a panel started with --config, as a dev copy
	// is, makes and serves fleets in the file it was given and no other.
	cfgPath := filepath.Join(home, "dev", "config.yaml")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	watches := &watchCount{n: map[string]int{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, runOpts{configPath: cfgPath, standSocket: filepath.Join(home, "no-daemon.sock"), watch: watches.watch})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	c := panelClient{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", cfg.ServerPort)}
	until(t, "the panel's own page", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/?fleet=", "")
		return code == http.StatusOK && strings.Contains(body, `id="board"`), fmt.Sprint(code, body)
	})
	until(t, "the first fleet's board watch", func() (bool, string) {
		return watches.of(firstBoard) == 1, fmt.Sprint(watches.of(firstBoard))
	})
	return startPanel{home: home, cfgPath: cfgPath, firstBoard: firstBoard, c: c, watches: watches}
}

func panelOnOneFleet(t *testing.T) (home string, c panelClient, cfgPath string) {
	t.Helper()
	p := panelOnOneFleetWatched(t)
	return p.home, p.c, p.cfgPath
}

// snapshotOf is the running panel's snapshot of one fleet, "" being the first.
func (p startPanel) snapshotOf(t *testing.T, name string) (int, state.Snapshot, string) {
	t.Helper()
	code, body := p.c.do(http.MethodGet, "/api/snapshot?fleet="+name, "")
	var snap state.Snapshot
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &snap); err != nil {
			t.Fatalf("the snapshot does not decode: %v\n%s", err, body)
		}
	}
	return code, snap, body
}

// untilCard waits for the card at path in the snapshot of the fleet named
// name, writing the card again once a second while it waits. A watch counted
// as started may not be listening yet, and the one write it missed must not be
// the only one; the poll is an hour away either way, so only a watch brings it
// in. Not on every ask: a write every 50 ms keeps resetting the watch's
// coalescing timer (board.Watch waits for 300 ms of quiet) and it never fires.
func (p startPanel) untilCard(t *testing.T, what, name, path string) {
	t.Helper()
	written := time.Now()
	until(t, what, func() (bool, string) {
		if time.Since(written) > time.Second {
			if raw, err := os.ReadFile(path); err == nil {
				_ = os.WriteFile(path, raw, 0o600)
			}
			written = time.Now()
		}
		_, snap, body := p.snapshotOf(t, name)
		return hasCard(snap, path), body
	})
}

func hasCard(snap state.Snapshot, path string) bool {
	return slices.ContainsFunc(snap.Cards, func(c board.Card) bool { return filepath.Clean(c.Path) == filepath.Clean(path) })
}

func makeVPN(t *testing.T, c panelClient, root string) {
	t.Helper()
	code, body := c.do(http.MethodPost, "/api/fleets", fmt.Sprintf(`{"name":"vpn","path":%q}`, root))
	if code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("fleet vpn was not made: %d %s", code, body)
	}
}

// The start page's write, end to end: a second fleet made by a running panel,
// on disk and in the configuration file.
func TestASecondFleetIsMadeFromTheStartPage(t *testing.T) {
	home, c, cfgPath := panelOnOneFleet(t)

	root := filepath.Join(home, "work", "vpn")
	makeVPN(t, c, root)

	for _, dir := range []string{filepath.Join(root, "board", "cards"), filepath.Join(root, "docs")} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("the fleet's own %s was not made: %v", dir, err)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("the configuration no longer loads after a fleet was added: %v", err)
	}
	names := []string{}
	for _, f := range cfg.FleetList() {
		names = append(names, f.Name)
	}
	if len(cfg.Fleets) != 1 || cfg.Fleets[0].Name != "vpn" {
		t.Fatalf("the fleet is not in the configuration: %v", names)
	}
	if filepath.Clean(cfg.Fleets[0].BoardPath) != filepath.Clean(filepath.Join(root, "board")) {
		t.Fatalf("the fleet's board is not the folder that was asked for: %q", cfg.Fleets[0].BoardPath)
	}
}

// The card's acceptance: the panel that made the fleet serves it the moment
// it answers — in the list of fleets, its snapshot, its documentation, a card
// started on its board, and an edit on that board brought in by a watch of its
// own — with no restart.
func TestAFleetMadeFromTheStartPageIsServedAtOnce(t *testing.T) {
	p := panelOnOneFleetWatched(t)
	root := filepath.Join(p.home, "work", "vpn")
	vpnBoard := workspace.BoardDir(root)

	makeVPN(t, p.c, root)

	code, snap, body := p.snapshotOf(t, "vpn")
	if code != http.StatusOK {
		t.Fatalf("the panel that made fleet vpn does not serve it: %d %s", code, body)
	}
	if snap.Fleet != "vpn" || !slices.Equal(snap.Fleets, []string{"obsidian", "vpn"}) {
		t.Fatalf("fleet %q of %v, want vpn of [obsidian vpn]", snap.Fleet, snap.Fleets)
	}
	if _, first, _ := p.snapshotOf(t, ""); !slices.Equal(first.Fleets, []string{"obsidian", "vpn"}) {
		t.Fatalf("the first fleet's tab lists %v, without the new fleet to switch to", first.Fleets)
	}

	if code, body := p.c.do(http.MethodGet, "/api/docs?fleet=vpn", ""); code != http.StatusOK {
		t.Fatalf("fleet vpn's documentation: %d %s", code, body)
	}

	code, body = p.c.do(http.MethodPost, "/api/cards?fleet=vpn", `{"title":"a task of vpn","zone":"planned"}`)
	var created struct {
		Path string `json:"path"`
	}
	if code != http.StatusCreated || json.Unmarshal([]byte(body), &created) != nil {
		t.Fatalf("a card on fleet vpn: %d %s", code, body)
	}
	if !strings.HasPrefix(created.Path, vpnBoard+string(filepath.Separator)) {
		t.Fatalf("fleet vpn's card went to %s, not onto %s", created.Path, vpnBoard)
	}

	// Written past the panel, as an agent writes: only vpn's own board watch
	// can bring it in before the next poll, an hour away.
	byHand, err := board.CreateCard(vpnBoard, "written by an agent", "urgent", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p.untilCard(t, "the card written on vpn's board in vpn's snapshot", "vpn", byHand)
	if n := p.watches.of(vpnBoard); n != 1 {
		t.Fatalf("fleet vpn's board is watched %d times, want once", n)
	}
}

// Making a fleet leaves the fleets already served alone: the first fleet
// keeps its board, its cards and its one watch, and that watch still works.
func TestMakingAFleetLeavesTheServedFleetsAlone(t *testing.T) {
	p := panelOnOneFleetWatched(t)

	before, err := board.CreateCard(p.firstBoard, "the first fleet's task", "urgent", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p.untilCard(t, "the first fleet's card", "", before)

	makeVPN(t, p.c, filepath.Join(p.home, "work", "vpn"))

	_, snap, body := p.snapshotOf(t, "")
	if snap.Fleet != "obsidian" || !hasCard(snap, before) {
		t.Fatalf("the first fleet after a fleet was made: %s", body)
	}
	if _, vpn, _ := p.snapshotOf(t, "vpn"); hasCard(vpn, before) {
		t.Fatal("the first fleet's card is shown on the new fleet's board")
	}
	if n := p.watches.of(p.firstBoard); n != 1 {
		t.Fatalf("the first fleet's board was watched %d times, want its one watch kept", n)
	}

	after, err := board.CreateCard(p.firstBoard, "written after", "urgent", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p.untilCard(t, "a card written on the first board after a fleet was made", "", after)
}

// Making the same fleet twice keeps what is there rather than refusing or
// making a second entry — the same way `init --fleet` run twice behaves — and
// the running panel neither lists it twice nor watches its board twice.
func TestMakingTheSameFleetTwiceKeepsIt(t *testing.T) {
	p := panelOnOneFleetWatched(t)
	root := filepath.Join(p.home, "work", "vpn")

	for range 2 {
		makeVPN(t, p.c, root)
	}
	cfg, err := config.Load(p.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Fleets) != 1 {
		t.Fatalf("the fleet was added twice: %+v", cfg.Fleets)
	}
	if _, snap, body := p.snapshotOf(t, "vpn"); !slices.Equal(snap.Fleets, []string{"obsidian", "vpn"}) {
		t.Fatalf("the running panel lists: %s", body)
	}
	if n := p.watches.of(workspace.BoardDir(root)); n != 1 {
		t.Fatalf("fleet vpn's board is watched %d times after it was made twice, want once", n)
	}
}

// The configuration changed by hand under a running panel is read at the next
// start, not now. So a fleet the file takes can still be one the running panel
// cannot serve — here vpn, removed from the file by hand while the panel
// serves it on another board — and that is said as a step that failed, not
// reported as a fleet that is made.
func TestAFleetTheRunningPanelCannotServeIsNotReportedAsMade(t *testing.T) {
	p := panelOnOneFleetWatched(t)
	served := filepath.Join(p.home, "work", "vpn")
	makeVPN(t, p.c, served)

	cfg, err := config.Load(p.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Fleets = nil
	if err := config.Save(p.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	elsewhere := filepath.Join(p.home, "work", "elsewhere")
	code, body := p.c.do(http.MethodPost, "/api/fleets", fmt.Sprintf(`{"name":"vpn","path":%q}`, elsewhere))
	var out struct {
		OK    bool `json:"ok"`
		Steps []struct {
			Name  string `json:"name"`
			Error string `json:"error"`
		} `json:"steps"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil {
		t.Fatalf("want 200 with the steps, got %d %s", code, body)
	}
	if out.OK {
		t.Fatalf("a fleet the running panel cannot serve was reported as made: %s", body)
	}
	i := slices.IndexFunc(out.Steps, func(s struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	}) bool {
		return s.Name == "panel"
	})
	if i < 0 || !strings.Contains(out.Steps[i].Error, "restart") {
		t.Fatalf("no panel step saying why it is not served and what brings it in: %s", body)
	}
	// Last: the page's verdict on a refused step says nothing after it was
	// done, and the statusline and the permission are written before it.
	if i != len(out.Steps)-1 {
		t.Fatalf("the panel step is step %d of %d, want the last: %s", i+1, len(out.Steps), body)
	}

	_, snap, body := p.snapshotOf(t, "vpn")
	if !slices.Equal(snap.Fleets, []string{"obsidian", "vpn"}) {
		t.Fatalf("the running panel after the refusal: %s", body)
	}
	if n := p.watches.of(workspace.BoardDir(elsewhere)); n != 0 {
		t.Fatalf("a board the panel does not serve is watched %d times", n)
	}
}

// A fleet added past the running panel — `fleetdeck init --fleet` from a
// terminal writes the same file — is not served by it: the panel does not
// watch its configuration, and only the fleets its own start page makes are
// taken in while it runs. The docs send such a fleet to a restart.
func TestAFleetInitAddsPastTheRunningPanelIsNotServedByIt(t *testing.T) {
	p := panelOnOneFleetWatched(t)
	root := filepath.Join(p.home, "work", "vpn")

	steps := fleetSteps(p.cfgPath, initEnv{home: p.home, workspace: root, config: p.cfgPath, fleet: "vpn"})
	if !fleetMade(steps) {
		t.Fatalf("init --fleet did not add the fleet: %+v", steps)
	}

	if code, body := p.c.do(http.MethodGet, "/api/snapshot?fleet=vpn", ""); code != http.StatusNotFound {
		t.Fatalf("the running panel serves a fleet init added past it: %d %s", code, body)
	}
	if _, snap, body := p.snapshotOf(t, ""); !slices.Equal(snap.Fleets, []string{"obsidian"}) {
		t.Fatalf("the running panel lists a fleet init added past it: %s", body)
	}
	if n := p.watches.of(workspace.BoardDir(root)); n != 0 {
		t.Fatalf("the board of a fleet the panel does not serve is watched %d times", n)
	}
}

// A name another fleet already has, on a different board, is refused — and it
// is refused as a step, not as a bad request: `init --fleet` puts the
// configuration's refusal in its config step, and this route runs the same
// steps. Pinned because the two look alike from the page and are answered
// differently: 200 with ok false here, 400 for a request that never got as far
// as a step.
func TestAFleetNameAnotherFleetHasIsRefusedAsAStep(t *testing.T) {
	home, c, cfgPath := panelOnOneFleet(t)

	makeVPN(t, c, filepath.Join(home, "work", "vpn"))

	again := fmt.Sprintf(`{"name":"vpn","path":%q}`, filepath.Join(home, "work", "elsewhere"))
	code, body := c.do(http.MethodPost, "/api/fleets", again)
	if code != http.StatusOK {
		t.Fatalf("want 200 with a refused step, got %d %s", code, body)
	}
	if !strings.Contains(body, `"ok":false`) {
		t.Fatalf("a name another fleet has must not report the fleet as made: %s", body)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Fleets) != 1 {
		t.Fatalf("the refused fleet was written anyway: %+v", cfg.Fleets)
	}
}

// The verdict is about every step that decides, not only the configuration's.
// A fleet already in the configuration whose folder cannot be made leaves the
// config step reporting "kept" with no error, beside a board step that failed:
// fleetSteps decides "kept" before it looks at the board. Reported as made,
// that would put "The fleet is made" directly above the line saying its
// workspace could not be.
func TestAFleetWhoseWorkspaceFailedIsNotReportedAsMade(t *testing.T) {
	steps := []initStep{
		{name: "workspace", err: errors.New("mkdir /x: permission denied")},
		{name: "config", note: "kept, fleet vpn is already there"},
		{name: "statusline", err: errors.New("no fleetdeck-status found")},
		{name: "permissions", note: "added"},
	}
	if fleetMade(steps) {
		t.Fatal("a fleet whose workspace step failed must not be reported as made")
	}

	// The control case: the same steps with the workspace made. A statusline
	// that could not be wired is reported and decides nothing.
	steps[0] = initStep{name: "workspace", note: "made"}
	if !fleetMade(steps) {
		t.Fatal("a refused statusline must not keep a made fleet from being reported as made")
	}
}

// A fleet with no name, and one with a folder that is not a full path, are
// refused before anything is made. Both are refusals of the request, not steps
// that failed: the page shows them beside the form rather than as a report.
func TestAFleetTheRequestItselfCannotNameIsRefused(t *testing.T) {
	home, c, cfgPath := panelOnOneFleet(t)

	for _, body := range []string{
		fmt.Sprintf(`{"name":"","path":%q}`, filepath.Join(home, "work", "vpn")),
		fmt.Sprintf(`{"name":"   ","path":%q}`, filepath.Join(home, "work", "vpn")),
		`{"name":"vpn","path":""}`,
		`{"name":"vpn","path":"relative/place"}`,
	} {
		if code, got := c.do(http.MethodPost, "/api/fleets", body); code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d %s", body, code, got)
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Fleets) != 0 {
		t.Fatalf("a refused request still wrote a fleet: %+v", cfg.Fleets)
	}
}
