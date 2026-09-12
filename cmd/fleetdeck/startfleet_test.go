package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
)

// A panel serving one fleet, the way the start page finds it.
func panelOnOneFleet(t *testing.T) (home string, c panelClient, cfgPath string) {
	t.Helper()
	home = isolateHome(t)
	board := filepath.Join(home, "obsidian", "board")
	if err := os.MkdirAll(filepath.Join(board, "cards"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.BoardPath = board
	cfg.UsageEnabled = false
	cfg.ServerPort = freePort(t)
	cfgPath = config.DefaultPath()
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, runOpts{configPath: cfgPath, standSocket: filepath.Join(home, "no-daemon.sock")})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	c = panelClient{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", cfg.ServerPort)}
	until(t, "the panel's own page", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/?fleet=", "")
		return code == http.StatusOK && strings.Contains(body, `id="board"`), fmt.Sprint(code, body)
	})
	return home, c, cfgPath
}

// The start page's write, end to end: a second fleet made by a running panel,
// on disk and in the configuration file.
func TestASecondFleetIsMadeFromTheStartPage(t *testing.T) {
	home, c, cfgPath := panelOnOneFleet(t)

	root := filepath.Join(home, "work", "vpn")
	code, body := c.do(http.MethodPost, "/api/fleets", fmt.Sprintf(`{"name":"vpn","path":%q}`, root))
	if code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("a second fleet was not made: %d %s", code, body)
	}

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

// And the panel does not serve it until it is restarted — the one thing the
// page promises about a new fleet, pinned here so a later change to how the
// configuration is read cannot quietly make the page's sentence a lie in
// either direction.
func TestAFleetMadeFromTheStartPageIsNotServedUntilARestart(t *testing.T) {
	home, c, _ := panelOnOneFleet(t)

	code, body := c.do(http.MethodPost, "/api/fleets", fmt.Sprintf(`{"name":"vpn","path":%q}`, filepath.Join(home, "work", "vpn")))
	if code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("a second fleet was not made: %d %s", code, body)
	}

	if code, body := c.do(http.MethodGet, "/api/snapshot?fleet=vpn", ""); code != http.StatusNotFound {
		t.Fatalf("the running panel already serves a fleet it never read: %d %s", code, body)
	}
	// The fleet it does serve is unchanged, and still the only one it lists.
	code, body = c.do(http.MethodGet, "/api/snapshot", "")
	var snap struct {
		Fleets []string `json:"fleets"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &snap) != nil {
		t.Fatalf("the panel stopped answering after a fleet was added: %d %s", code, body)
	}
	if len(snap.Fleets) > 1 {
		t.Fatalf("the running panel picked up a fleet it was never restarted for: %v", snap.Fleets)
	}
}

// Making the same fleet twice keeps what is there rather than refusing or
// making a second entry — the same way `init --fleet` run twice behaves.
func TestMakingTheSameFleetTwiceKeepsIt(t *testing.T) {
	home, c, cfgPath := panelOnOneFleet(t)

	body := fmt.Sprintf(`{"name":"vpn","path":%q}`, filepath.Join(home, "work", "vpn"))
	for i := range 2 {
		if code, got := c.do(http.MethodPost, "/api/fleets", body); code != http.StatusOK || !strings.Contains(got, `"ok":true`) {
			t.Fatalf("attempt %d: %d %s", i+1, code, got)
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Fleets) != 1 {
		t.Fatalf("the fleet was added twice: %+v", cfg.Fleets)
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

	first := fmt.Sprintf(`{"name":"vpn","path":%q}`, filepath.Join(home, "work", "vpn"))
	if code, got := c.do(http.MethodPost, "/api/fleets", first); code != http.StatusOK || !strings.Contains(got, `"ok":true`) {
		t.Fatalf("the first fleet was not made: %d %s", code, got)
	}

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
