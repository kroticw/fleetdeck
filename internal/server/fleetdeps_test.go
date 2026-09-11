package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/fleet"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// fleetBoards lays out two fleets' boards and docs, each with one card and one
// document, and returns their directories.
func fleetBoards(t *testing.T) (aBoard, aDocs, bBoard, bDocs string) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		for _, dir := range []string{"board/cards", "docs"} {
			if err := os.MkdirAll(filepath.Join(root, name, dir), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		card := "---\nzone: planned\nstage: active\nprogress: 20\n---\n\n# " + name + "\n"
		if err := os.WriteFile(filepath.Join(root, name, "board/cards", name+".md"), []byte(card), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "docs", name+"-doc.md"), []byte("# "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "a/board"), filepath.Join(root, "a/docs"), filepath.Join(root, "b/board"), filepath.Join(root, "b/docs")
}

// twoFleetWrites is a panel with two fleets: its own fields are fleet A's, as
// a one-fleet panel always had them, and Fleet answers for either by name.
func twoFleetWrites(t *testing.T) (Deps, *[]string, [4]string) {
	t.Helper()
	d, calls := testDeps()
	aBoard, aDocs, bBoard, bDocs := fleetBoards(t)
	per := func(name, board, docs string) FleetDeps {
		return FleetDeps{
			BoardDir:  board,
			DocsRoots: []string{docs},
			CreateCard: func(title, _ string) (string, error) {
				*calls = append(*calls, "create:"+name+":"+title)
				return filepath.Join(board, "cards", "new.md"), nil
			},
			SetOrchestratorSession: func(id string) error {
				*calls = append(*calls, "pin:"+name+":"+id)
				if id == "taken001" {
					return fmt.Errorf("%w: fleets[0]: orchestrator %q is already the orchestrator of the top-level fleet", ErrOrchestratorTaken, id)
				}
				return nil
			},
			OrchestratorPreview: func(string) (orchestrator.Preview, error) {
				return orchestrator.Preview{Path: filepath.Join(docs, "orchestrator.md"), Workspace: filepath.Dir(board)}, nil
			},
			Appoint: func(_ context.Context, req orchestrator.Request) (orchestrator.Result, error) {
				*calls = append(*calls, "appoint:"+name+":"+req.Session)
				return orchestrator.Result{Session: req.Session, OK: true}, nil
			},
		}
	}
	a, b := per("A", aBoard, aDocs), per("B", bBoard, bDocs)
	d.BoardDir, d.DocsRoots, d.CreateCard, d.SetOrchestratorSession = a.BoardDir, a.DocsRoots, a.CreateCard, a.SetOrchestratorSession
	d.OrchestratorPreview, d.Appoint = a.OrchestratorPreview, a.Appoint
	d.Fleet = func(name string) (FleetDeps, error) {
		switch name {
		case "", "A":
			return a, nil
		case "B":
			return b, nil
		}
		return FleetDeps{}, fmt.Errorf("%w %q; the fleets are \"A\", \"B\"", fleet.ErrUnknown, name)
	}
	return d, calls, [4]string{aBoard, aDocs, bBoard, bDocs}
}

func TestANewCardGoesOnTheBoardOfTheTabsFleet(t *testing.T) {
	d, calls, _ := twoFleetWrites(t)
	rec := do(d, http.MethodPost, "/api/cards?fleet=B", `{"title":"B task","zone":"planned"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body)
	}
	if strings.Join(*calls, ",") != "create:B:B task" {
		t.Fatalf("the card was started as %v, want on B's board", *calls)
	}
}

func TestACardWriteIsConfinedToTheTabsFleetsBoard(t *testing.T) {
	d, calls, dirs := twoFleetWrites(t)
	bCard := filepath.Join(dirs[2], "cards", "b.md")
	aCard := filepath.Join(dirs[0], "cards", "a.md")
	if rec := do(d, http.MethodPatch, "/api/cards?fleet=B", `{"path":"`+bCard+`","field":"stage","value":"review"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("B's own card: want 204, got %d: %s", rec.Code, rec.Body)
	}
	if rec := do(d, http.MethodPatch, "/api/cards?fleet=B", `{"path":"`+aCard+`","field":"stage","value":"review"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("A's card through B's tab: want 403, got %d: %s", rec.Code, rec.Body)
	}
	if len(*calls) != 1 || !strings.HasSuffix((*calls)[0], "b.md:stage:review") {
		t.Fatalf("card writes = %v, want only B's", *calls)
	}
}

func TestTheDocumentationIsTheTabsFleets(t *testing.T) {
	d, _, _ := twoFleetWrites(t)
	rec := do(d, http.MethodGet, "/api/docs?fleet=B", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "b-doc.md") || strings.Contains(rec.Body.String(), "a-doc.md") {
		t.Fatalf("B's documentation: %d %s", rec.Code, rec.Body)
	}
	rec = do(d, http.MethodGet, "/api/docs", "")
	if !strings.Contains(rec.Body.String(), "a-doc.md") || strings.Contains(rec.Body.String(), "b-doc.md") {
		t.Fatalf("a tab naming no fleet must get the first fleet's documentation: %s", rec.Body)
	}
}

func TestTheDocumentContentIsConfinedToTheTabsFleetsDocs(t *testing.T) {
	d, _, dirs := twoFleetWrites(t)
	aDoc := filepath.Join(dirs[1], "a-doc.md")
	rec := do(d, http.MethodGet, "/api/docs/content?fleet=B&path="+aDoc, "")
	if rec.Code == http.StatusOK {
		t.Fatalf("A's document was served through B's tab: %s", rec.Body)
	}
}

func TestPinningGoesToTheTabsFleet(t *testing.T) {
	d, calls, _ := twoFleetWrites(t)
	if rec := do(d, http.MethodPatch, "/api/config?fleet=B", `{"orchestratorSession":"cafe0001"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body)
	}
	if strings.Join(*calls, ",") != "pin:B:cafe0001" {
		t.Fatalf("pins = %v, want B's", *calls)
	}
}

func TestPinningAnotherFleetsOrchestratorIsAConflict(t *testing.T) {
	d, _, _ := twoFleetWrites(t)
	rec := do(d, http.MethodPatch, "/api/config?fleet=B", `{"orchestratorSession":"taken001"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "already the orchestrator") {
		t.Fatalf("the refusal must say why: %s", rec.Body)
	}
}

func TestTheWizardAppointsForTheTabsFleet(t *testing.T) {
	d, calls, dirs := twoFleetWrites(t)
	rec := do(d, http.MethodGet, "/api/orchestrator?fleet=B&lang=en", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), filepath.Dir(dirs[2])) {
		t.Fatalf("B's preview: %d %s", rec.Code, rec.Body)
	}
	rec = do(d, http.MethodPost, "/api/orchestrator?fleet=B", `{"session":"cafe0002","lang":"en"}`)
	if rec.Code != http.StatusOK || strings.Join(*calls, ",") != "appoint:B:cafe0002" {
		t.Fatalf("appoint: %d %s, calls %v", rec.Code, rec.Body, *calls)
	}
}

func TestEveryFleetRouteRefusesAnUnknownFleet(t *testing.T) {
	d, calls, _ := twoFleetWrites(t)
	for _, r := range []struct{ method, target, body string }{
		{http.MethodPost, "/api/cards?fleet=C", `{"title":"x","zone":"planned"}`},
		{http.MethodPatch, "/api/cards?fleet=C", `{"path":"/x.md","field":"stage","value":"done"}`},
		{http.MethodGet, "/api/docs?fleet=C", ""},
		{http.MethodGet, "/api/docs/content?fleet=C&path=/x.md", ""},
		{http.MethodPatch, "/api/config?fleet=C", `{"orchestratorSession":"cafe0003"}`},
		{http.MethodGet, "/api/orchestrator?fleet=C&lang=en", ""},
		{http.MethodPost, "/api/orchestrator?fleet=C", `{"session":"cafe0004","lang":"en"}`},
	} {
		rec := do(d, r.method, r.target, r.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: want 404, got %d: %s", r.method, r.target, rec.Code, rec.Body)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a request for an unknown fleet reached %v", *calls)
	}
}
