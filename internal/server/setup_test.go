package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupDeps() (SetupDeps, *[]string) {
	var calls []string
	return SetupDeps{
		DefaultWorkspace: "/home/x/fleetdeck",
		Setup: func(path string) ([]SetupStep, bool, error) {
			calls = append(calls, path)
			return []SetupStep{
				{Name: "config", Note: "/home/x/.config/fleetdeck/config.yaml (created)"},
				{Name: "workspace", Note: path + " (board created, docs created)"},
				{Name: "statusline", Error: "no fleetdeck-status found"},
			}, true, nil
		},
	}, &calls
}

func setupDo(_ SetupDeps, method, target, body string) *http.Request {
	if body == "" {
		return httptest.NewRequest(method, target, nil)
	}
	return jsonRequest(method, target, body)
}

func serveSetup(sd SetupDeps, r *http.Request) (int, string, http.Header) {
	rec := httptest.NewRecorder()
	NewSetup(sd).ServeHTTP(rec, r)
	return rec.Code, rec.Body.String(), rec.Header()
}

func TestSetupServesTheSetupPageAtTheRoot(t *testing.T) {
	sd, _ := setupDeps()
	code, body, header := serveSetup(sd, setupDo(sd, http.MethodGet, "/", ""))
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if !strings.Contains(body, `id="setup"`) {
		t.Fatalf("the root must be the setup page, got:\n%.300s", body)
	}
	if header.Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Fatal("the setup page must carry the same Content-Security-Policy as the panel")
	}
	// The panel's own page at "/" carries the build's ETag. If this page carried
	// it too, the reload after setup would be answered 304 and the browser would
	// show this page again instead of the panel.
	if header.Get("ETag") != "" {
		t.Fatalf("the setup page must carry no ETag, got %q", header.Get("ETag"))
	}
}

func TestSetupServesTheStylesAndScriptsTheSetupPageNeeds(t *testing.T) {
	sd, _ := setupDeps()
	for _, path := range []string{"/app.css", "/js/setup.js", "/js/i18n.js"} {
		if code, _, _ := serveSetup(sd, setupDo(sd, http.MethodGet, path, "")); code != http.StatusOK {
			t.Errorf("%s: want 200, got %d", path, code)
		}
	}
}

func TestSetupNamesTheDefaultWorkspace(t *testing.T) {
	sd, _ := setupDeps()
	code, body, _ := serveSetup(sd, setupDo(sd, http.MethodGet, "/api/setup", ""))
	if code != http.StatusOK || !strings.Contains(body, `"default":"/home/x/fleetdeck"`) {
		t.Fatalf("want the default workspace, got %d %s", code, body)
	}
}

func TestSetupCreatesTheWorkspaceAndReportsEveryStep(t *testing.T) {
	sd, calls := setupDeps()
	code, body, _ := serveSetup(sd, setupDo(sd, http.MethodPost, "/api/setup", `{"path":"/srv/fleet"}`))
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", code, body)
	}
	if len(*calls) != 1 || (*calls)[0] != "/srv/fleet" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
	for _, want := range []string{`"ok":true`, `"name":"workspace"`, "no fleetdeck-status found"} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
}

func TestSetupReportsARefusedPathAsBadRequest(t *testing.T) {
	sd, _ := setupDeps()
	sd.Setup = func(string) ([]SetupStep, bool, error) {
		return nil, false, errors.New("give an absolute path")
	}
	code, body, _ := serveSetup(sd, setupDo(sd, http.MethodPost, "/api/setup", `{"path":"fleet"}`))
	if code != http.StatusBadRequest || !strings.Contains(body, "absolute") {
		t.Fatalf("want 400 naming the problem, got %d %s", code, body)
	}
}

// The panel's routes do not exist yet. A page left open from before — or the
// panel's own page opened by hand — gets words, not a 404 that reads as a
// broken panel.
func TestSetupAnswersThePanelsRoutesWithUnavailable(t *testing.T) {
	sd, _ := setupDeps()
	for _, target := range []string{"/api/snapshot", "/ws", "/api/docs"} {
		code, body, _ := serveSetup(sd, setupDo(sd, http.MethodGet, target, ""))
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "set up") {
			t.Errorf("%s: want 503 saying the panel is not set up, got %d %s", target, code, body)
		}
	}
	code, _, _ := serveSetup(sd, setupDo(sd, http.MethodPost, "/api/cards", `{"title":"x","zone":"planned"}`))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("a card cannot be created before there is a board: got %d", code)
	}
}

// Setup writes into the person's home directory — the configuration and
// Claude Code's settings — so a page on another site must not be able to
// trigger it.
func TestSetupRefusesAForeignPage(t *testing.T) {
	sd, calls := setupDeps()
	r := setupDo(sd, http.MethodPost, "/api/setup", `{"path":"/srv/fleet"}`)
	r.Header.Set("Origin", "https://evil.example")
	if code, _, _ := serveSetup(sd, r); code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("a foreign page ran setup: %v", *calls)
	}
}

func TestSetupRefusesAnyFieldButThePath(t *testing.T) {
	sd, calls := setupDeps()
	code, _, _ := serveSetup(sd, setupDo(sd, http.MethodPost, "/api/setup", `{"path":"/srv/fleet","force":true}`))
	if code != http.StatusBadRequest || len(*calls) != 0 {
		t.Fatalf("want 400 and no setup, got %d, calls %v", code, *calls)
	}
}
