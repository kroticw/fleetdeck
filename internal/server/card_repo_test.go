package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A card's repo is a folder a worker can start in, and nothing else (T-134):
// T-132 reached the board with its task in the repo field, and a repo that is
// no folder is caught where it is written, in words the page can say, not at
// the dispatch a day later.
func repoHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "src", "fleetdeck"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func refusalCode(t *testing.T, body []byte) string {
	t.Helper()
	var answer struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		t.Fatalf("the refusal is not JSON: %s", body)
	}
	if answer.Error == "" {
		t.Fatalf("the refusal must say why in words too: %s", body)
	}
	return answer.Code
}

func TestCreateCardRefusesARepoThatIsNoFolder(t *testing.T) {
	d, calls := createDeps()
	d.Home = repoHome(t)
	rec := do(d, http.MethodPost, "/api/cards", `{"title":"анализ дискового пространства","zone":"unplanned","repo":"диск забит под завязку, нужно провести анализ, чем"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := refusalCode(t, rec.Body.Bytes()); code != "repo_not_a_directory" {
		t.Fatalf("want the code repo_not_a_directory, got %q", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused card reached the board: %v", *calls)
	}
}

func TestCreateCardTakesAFolderUnderHomeAndHomeItself(t *testing.T) {
	for _, repo := range []string{"src/fleetdeck", "~/src/fleetdeck", "", "~"} {
		d, calls := createDeps()
		d.Home = repoHome(t)
		rec := do(d, http.MethodPost, "/api/cards", `{"title":"A task","zone":"planned","repo":"`+repo+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("repo %q: want 201, got %d: %s", repo, rec.Code, rec.Body.String())
		}
		if len(*calls) != 1 {
			t.Fatalf("repo %q: unexpected calls: %v", repo, *calls)
		}
	}
}

func TestPatchCardRefusesARepoThatIsNoFolder(t *testing.T) {
	d, calls, _ := cardDeps(t)
	d.Home = repoHome(t)
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"repo","value":"src/gone"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := refusalCode(t, rec.Body.Bytes()); code != "repo_not_a_directory" {
		t.Fatalf("want the code repo_not_a_directory, got %q", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused repo reached the card: %v", *calls)
	}
}

func TestPatchCardRefusesARepoOutsideHomeWithACode(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.Home = repoHome(t)
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"repo","value":"/etc"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := refusalCode(t, rec.Body.Bytes()); code != "repo_outside_home" {
		t.Fatalf("want the code repo_outside_home, got %q", code)
	}
}

func TestPatchCardTakesTheHomeDirectoryAsARepo(t *testing.T) {
	d, calls, card := cardDeps(t)
	d.Home = repoHome(t)
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"repo","value":"~"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "card:"+card+":repo:~" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}
