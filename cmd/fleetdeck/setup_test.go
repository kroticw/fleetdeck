package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/state"
)

// isolateHome points everything the panel's setup writes — the configuration,
// Claude Code's settings, the board's git — at a temporary home, and gives git
// an identity there so the board's commits need neither the machine's config
// nor its signing key. The daemon is kept away separately, by standSocket.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "no-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"GIT_AUTHOR", "GIT_COMMITTER"} {
		t.Setenv(name+"_NAME", "T")
		t.Setenv(name+"_EMAIL", "t@example.com")
	}
	return home
}

type panelClient struct {
	t    *testing.T
	base string
}

func (c panelClient) do(method, path, body string) (int, string) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// until asks f every 50 ms for up to 10 s and fails the test with what it last
// saw if f never says yes.
func until(t *testing.T, what string, f func() (bool, string)) {
	t.Helper()
	last := ""
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		ok, seen := f()
		if ok {
			return
		}
		last = seen
	}
	t.Fatalf("%s did not happen; last seen: %s", what, last)
}

// The card's acceptance, end to end in one process: a machine with no board and
// no configuration starts the panel, the panel asks for a folder, makes an empty
// board there, and a card started from the panel appears on it.
func TestAPanelWithNoConfigurationIsSetUpFromItsPage(t *testing.T) {
	home := isolateHome(t)
	cfgPath := config.DefaultPath()
	if !strings.HasPrefix(cfgPath, home) {
		t.Fatalf("the configuration must be looked for in the test's home, not %s", cfgPath)
	}
	root := filepath.Join(t.TempDir(), "work", "fleet")
	port := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, runOpts{configPath: cfgPath, standSocket: filepath.Join(home, "no-daemon.sock"), port: port})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the panel stopped with %v", err)
		}
	}()
	c := panelClient{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port)}

	until(t, "the setup page", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/", "")
		return code == http.StatusOK && strings.Contains(body, `id="setup"`), fmt.Sprint(code, body)
	})
	if code, _ := c.do(http.MethodGet, "/api/snapshot", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("before setup the panel has no snapshot to give: got %d", code)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("showing the setup page must write nothing: %v", err)
	}

	code, body := c.do(http.MethodPost, "/api/setup", `{"path":"`+root+`"}`)
	if code != http.StatusOK {
		t.Fatalf("setup: %d %s", code, body)
	}
	var answer struct {
		OK    bool `json:"ok"`
		Steps []struct {
			Name, Note, Error string
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatal(err)
	}
	if !answer.OK {
		t.Fatalf("setup did not succeed: %s", body)
	}
	var names []string
	for _, s := range answer.Steps {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "config,workspace,statusline,permissions" {
		t.Fatalf("setup must report the same steps init does, got %s", got)
	}

	var snap state.Snapshot
	until(t, "the panel taking over", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/api/snapshot", "")
		if code != http.StatusOK {
			return false, fmt.Sprint(code, body)
		}
		return json.Unmarshal([]byte(body), &snap) == nil, body
	})
	if snap.BoardError != "" {
		t.Fatalf("a new board must not be reported as broken: %s", snap.BoardError)
	}
	if len(snap.Cards) != 0 {
		t.Fatalf("a new board is empty, got %d cards", len(snap.Cards))
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BoardPath != filepath.Join(root, "board") {
		t.Fatalf("the configuration names %s, want the chosen workspace's board", cfg.BoardPath)
	}

	code, body = c.do(http.MethodPost, "/api/cards", `{"title":"Первая карточка","zone":"planned"}`)
	if code != http.StatusCreated || !strings.Contains(body, `"committed":true`) {
		t.Fatalf("a card started on the new board: %d %s", code, body)
	}
	until(t, "the card on the board", func() (bool, string) {
		_, body := c.do(http.MethodGet, "/api/snapshot", "")
		var s state.Snapshot
		if json.Unmarshal([]byte(body), &s) != nil || len(s.Cards) != 1 {
			return false, body
		}
		return s.Cards[0].Title == "Первая карточка" && s.Cards[0].Stage == "new", body
	})

	// The setup surface is gone: the root is the start page now, and the panel
	// is behind a named fleet (internal/server/static.go). Both are asserted,
	// because "no longer the wizard" and "the panel is reachable" are two
	// facts, and a handover that left only the first would be a panel nobody
	// can open.
	code, body = c.do(http.MethodGet, "/", "")
	if code != http.StatusOK || !strings.Contains(body, `id="start"`) {
		t.Fatalf("after setup the root is the start page: %d %.200s", code, body)
	}
	code, body = c.do(http.MethodGet, "/?fleet=", "")
	if code != http.StatusOK || !strings.Contains(body, `id="board"`) {
		t.Fatalf("after setup the panel is served for its fleet: %d %.200s", code, body)
	}
	if code, _ := c.do(http.MethodPost, "/api/setup", `{"path":"/elsewhere"}`); code == http.StatusOK {
		t.Fatal("a panel that is set up must not be set up a second time from its page")
	}
}

// A folder that cannot be made keeps the panel in setup, and the person can
// then choose another one from the same page.
func TestASetupThatCannotMakeTheBoardCanBeCorrected(t *testing.T) {
	home := isolateHome(t)
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, runOpts{configPath: config.DefaultPath(), standSocket: filepath.Join(home, "no-daemon.sock"), port: port})
	}()
	defer func() {
		cancel()
		<-done
	}()
	c := panelClient{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	until(t, "the setup page", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/api/setup", "")
		return code == http.StatusOK, body
	})

	code, body := c.do(http.MethodPost, "/api/setup", `{"path":"`+filepath.Join(blocker, "ws")+`"}`)
	if code != http.StatusOK || !strings.Contains(body, `"ok":false`) {
		t.Fatalf("a workspace under a file cannot be made: %d %s", code, body)
	}
	if code, _ := c.do(http.MethodGet, "/api/snapshot", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("a failed setup must leave the panel in setup, got %d", code)
	}

	good := filepath.Join(t.TempDir(), "ws")
	code, body = c.do(http.MethodPost, "/api/setup", `{"path":"`+good+`"}`)
	if code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("the corrected folder must be accepted: %d %s", code, body)
	}
	until(t, "the panel taking over", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/api/snapshot", "")
		return code == http.StatusOK, body
	})
}

// The panel reads the file its -config flag names, so setup writes that file —
// not the default one under the home directory.
func TestSetupWritesTheConfigurationThePanelReads(t *testing.T) {
	home := isolateHome(t)
	cfgPath := filepath.Join(t.TempDir(), "elsewhere", "config.yaml")
	ready := make(chan struct{})
	_, ok, err := setupDeps(cfgPath, ready).Setup(filepath.Join(t.TempDir(), "ws"))
	if err != nil || !ok {
		t.Fatalf("setup: ok %v, %v", ok, err)
	}
	if _, err := config.Load(cfgPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("the configuration the panel reads was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "fleetdeck", "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("setup wrote the default configuration instead of the panel's own")
	}
}

// Between a successful setup and the panel taking over, a second request can
// still reach the setup surface; it must not run setup again.
func TestSetupIsRefusedOnceItHasSucceeded(t *testing.T) {
	isolateHome(t)
	ready := make(chan struct{})
	sd := setupDeps(filepath.Join(t.TempDir(), "config.yaml"), ready)
	if _, ok, err := sd.Setup(filepath.Join(t.TempDir(), "ws")); err != nil || !ok {
		t.Fatalf("first setup: ok %v, %v", ok, err)
	}
	select {
	case <-ready:
	default:
		t.Fatal("a successful setup must release the panel")
	}
	if _, ok, err := sd.Setup(filepath.Join(t.TempDir(), "other")); err == nil || ok {
		t.Fatalf("a second setup must be refused, got ok %v, %v", ok, err)
	}
}

// Without a configuration there is no panel to become, whatever else was made.
func TestASetupWhoseConfigurationCannotBeWrittenDoesNotReleaseThePanel(t *testing.T) {
	isolateHome(t)
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	steps, ok, err := setupDeps(filepath.Join(blocker, "config.yaml"), ready).Setup(filepath.Join(t.TempDir(), "ws"))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("setup reported success with no configuration: %+v", steps)
	}
	select {
	case <-ready:
		t.Fatal("the panel was released with no configuration to run on")
	default:
	}
}

func TestWorkspacePathIsMadeAbsoluteOrRefused(t *testing.T) {
	for in, want := range map[string]string{
		"/srv/fleet":      "/srv/fleet",
		"  /srv/fleet/  ": "/srv/fleet",
		"~":               "/home/x",
		"~/fleetdeck":     "/home/x/fleetdeck",
	} {
		got, err := workspacePath(in, "/home/x")
		if err != nil || got != want {
			t.Errorf("workspacePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "fleet", "./fleet", "~other/fleet"} {
		if got, err := workspacePath(in, "/home/x"); err == nil {
			t.Errorf("workspacePath(%q) = %q; a path relative to wherever the panel started must be refused", in, got)
		}
	}
}

// A panel that has a configuration never offers setup: the operator's own
// case — board.path names a board that is not touched.
func TestAPanelWithAConfigurationNeverShowsSetup(t *testing.T) {
	home := isolateHome(t)
	board := filepath.Join(home, "obsidian", "board")
	if err := os.MkdirAll(filepath.Join(board, "cards"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.BoardPath = board
	cfg.UsageEnabled = false
	cfg.ServerPort = freePort(t)
	cfgPath := config.DefaultPath()
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, runOpts{configPath: cfgPath, standSocket: filepath.Join(home, "no-daemon.sock")})
	}()
	defer func() {
		cancel()
		<-done
	}()
	c := panelClient{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", cfg.ServerPort)}
	until(t, "the panel's own page", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/?fleet=", "")
		return code == http.StatusOK && strings.Contains(body, `id="board"`), fmt.Sprint(code, body)
	})
	// And the root is the start page, never the wizard: a configured panel
	// offering setup is what this test exists to catch, and the start page
	// sits where the wizard used to be reachable from.
	if code, root := c.do(http.MethodGet, "/", ""); code != http.StatusOK || !strings.Contains(root, `id="start"`) {
		t.Fatalf("the root of a configured panel is the start page: %d %.200s", code, root)
	}
	if code, _ := c.do(http.MethodPost, "/api/setup", `{"path":"/elsewhere"}`); code == http.StatusOK {
		t.Fatal("a configured panel must not offer setup")
	}
	entries, _ := os.ReadDir(board)
	if len(entries) != 1 {
		t.Fatalf("the configured board was changed: %v", entries)
	}
}
