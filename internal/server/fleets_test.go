package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A refusal of the request itself, worded the way cmd/fleetdeck words it.
var errNotAFullPath = errors.New(`"vpn" is not a full path: give one starting with / or ~/`)

func postFleet(t *testing.T, d Deps, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fleets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	New(d).ServeHTTP(rec, req)
	return rec
}

// A stand is given no way to make a fleet, and the page shows that instead of
// a button that answers an error every time it is pressed.
func TestMakingAFleetOnAPanelWiredWithoutOneIs503(t *testing.T) {
	d, _ := testDeps()
	rec := postFleet(t, d, `{"name":"vpn","path":"~/vpn"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMakingAFleetPassesTheNameAndTheFolderThrough(t *testing.T) {
	d, _ := testDeps()
	var gotName, gotPath string
	d.CreateFleet = func(name, path string) ([]SetupStep, bool, error) {
		gotName, gotPath = name, path
		return []SetupStep{{Name: "config", Note: "fleet vpn added"}}, true, nil
	}
	rec := postFleet(t, d, `{"name":"vpn","path":"~/vpn"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotName != "vpn" || gotPath != "~/vpn" {
		t.Fatalf("name/path did not reach the write: %q %q", gotName, gotPath)
	}
	var got struct {
		OK    bool        `json:"ok"`
		Steps []SetupStep `json:"steps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || len(got.Steps) != 1 || got.Steps[0].Name != "config" {
		t.Fatalf("the steps did not reach the page: %+v", got)
	}
}

// A step that was refused comes back with its reason and ok false — the same
// report the wizard shows, because it is the same kind of write.
func TestARefusedStepIsReportedRatherThanTurnedIntoAnError(t *testing.T) {
	d, _ := testDeps()
	d.CreateFleet = func(string, string) ([]SetupStep, bool, error) {
		return []SetupStep{{Name: "board", Error: "mkdir /x: permission denied"}}, false, nil
	}
	rec := postFleet(t, d, `{"name":"vpn","path":"/x"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 with a refused step in the body, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "permission denied") || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("the refusal is not in the body: %s", rec.Body.String())
	}
}

// The request itself being refused — an empty name, a relative folder — is a
// 400 with the reason, and nothing was made.
func TestARefusedRequestIs400WithItsReason(t *testing.T) {
	d, _ := testDeps()
	d.CreateFleet = func(string, string) ([]SetupStep, bool, error) {
		return nil, false, errNotAFullPath
	}
	rec := postFleet(t, d, `{"name":"vpn","path":"vpn"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	// Decoded, not matched as a substring: the reason carries quotes of its
	// own, and JSON escapes them.
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error != errNotAFullPath.Error() {
		t.Fatalf("the reason reached the page reworded: %q", got.Error)
	}
}

// The same guard as every other write: a body that is not JSON is refused
// before the handler, so a form on another page cannot post one.
func TestMakingAFleetNeedsAJSONBody(t *testing.T) {
	d, _ := testDeps()
	d.CreateFleet = func(string, string) ([]SetupStep, bool, error) {
		t.Fatal("a non-JSON body reached the write")
		return nil, false, nil
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fleets", strings.NewReader("name=vpn"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	New(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d", rec.Code)
	}
}

// Making a fleet is a write into the person's home directory, so a page on
// another site must not be able to trigger it.
func TestMakingAFleetIsRefusedFromAForeignOrigin(t *testing.T) {
	d, _ := testDeps()
	d.CreateFleet = func(string, string) ([]SetupStep, bool, error) {
		t.Fatal("a foreign origin reached the write")
		return nil, false, nil
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/fleets", strings.NewReader(`{"name":"vpn","path":"~/vpn"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	New(d).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}
