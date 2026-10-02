package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/fleet"
)

func loadText(t *testing.T, content string) (Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

// The shape of the operator's own configuration, with no fleets and no name:
// it must keep loading, as exactly one fleet named after the folder above its
// board.
const operatorShape = `board:
    path: /Users/me/obsidian/board
docs:
    paths:
        - /Users/me/obsidian/board/docs
orchestrator:
    session: 06a1f607
notify:
    enabled:
        waiting: true
        failed: true
        silent: true
        card_blocked: true
    silence_after: 30m0s
daemon:
    poll_interval: 2s
usage:
    enabled: true
server:
    port: 7777
session_labels:
    06a1f607-1a29-4fb4-a02c-f1e7c5cf59a8: оркестр
`

func TestAConfigWithoutFleetsIsOneFleet(t *testing.T) {
	c, err := loadText(t, operatorShape)
	if err != nil {
		t.Fatalf("the operator's configuration no longer loads: %v", err)
	}
	want := []fleet.Fleet{{
		Name:         "obsidian",
		BoardPath:    "/Users/me/obsidian/board",
		DocsPaths:    []string{"/Users/me/obsidian/board/docs"},
		Orchestrator: "06a1f607",
	}}
	if got := c.FleetList(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FleetList() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestAConfigWithNoFileIsOneFleetNamedMain(t *testing.T) {
	got := Default().FleetList()
	if len(got) != 1 || got[0].Name != "main" || got[0].BoardPath != "" {
		t.Fatalf("defaults must be one board-less fleet named main, got %+v", got)
	}
}

const twoFleetShape = operatorShape + `name: основной
fleets:
    - name: clining
      board:
        path: /Users/me/clining/board
      docs:
        paths:
            - /Users/me/clining/docs
      orchestrator:
        session: 1a2b3c4d
    - name: work
      board:
        path: /Users/me/work/board
`

func TestFleetsFollowTheTopLevelFleet(t *testing.T) {
	c, err := loadText(t, twoFleetShape)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []fleet.Fleet{
		{Name: "основной", BoardPath: "/Users/me/obsidian/board", DocsPaths: []string{"/Users/me/obsidian/board/docs"}, Orchestrator: "06a1f607"},
		{Name: "clining", BoardPath: "/Users/me/clining/board", DocsPaths: []string{"/Users/me/clining/docs"}, Orchestrator: "1a2b3c4d"},
		{Name: "work", BoardPath: "/Users/me/work/board"},
	}
	if got := c.FleetList(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FleetList() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestTopLevelFieldsStillMeanTheFirstFleet(t *testing.T) {
	// Nothing that reads the top-level keys today is moved onto the list:
	// they stay the first fleet's, read exactly as before.
	c, err := loadText(t, twoFleetShape)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BoardPath != "/Users/me/obsidian/board" || c.OrchestratorSession != "06a1f607" {
		t.Fatalf("top-level keys moved: board %q orchestrator %q", c.BoardPath, c.OrchestratorSession)
	}
}

func TestFleetsRefuseMistakesAndSayWhereTheyAre(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			"a misspelt key inside a fleet",
			operatorShape + "fleets:\n    - name: clining\n      boardd:\n        path: /x/board\n",
			`unknown configuration key "boardd"`,
		},
		{
			"a fleet without a board",
			operatorShape + "fleets:\n    - name: clining\n",
			"fleets[0]: board path must be set",
		},
		{
			"a fleet without a name",
			operatorShape + "fleets:\n    - board:\n        path: /x/board\n",
			"fleets[0]: name must not be empty",
		},
		{
			"two fleets with one name",
			operatorShape + "fleets:\n    - name: clining\n      board:\n        path: /x/board\n    - name: clining\n      board:\n        path: /y/board\n",
			`fleets[1]: name "clining" is already the name of fleets[0]`,
		},
		{
			"a fleet named like the unnamed top-level fleet",
			operatorShape + "fleets:\n    - name: obsidian\n      board:\n        path: /x/board\n",
			`fleets[0]: name "obsidian" is already the name of the top-level fleet; give the top-level fleet a name with the top-level name key`,
		},
		{
			"the top-level orchestrator pinned in a second fleet",
			operatorShape + "fleets:\n    - name: clining\n      board:\n        path: /x/board\n      orchestrator:\n        session: 06a1f607\n",
			`fleets[0]: orchestrator "06a1f607" is already the orchestrator of the top-level fleet ("obsidian")`,
		},
		{
			"a fleet on the top-level fleet's board",
			operatorShape + "fleets:\n    - name: clining\n      board:\n        path: /Users/me/obsidian/board/\n",
			`fleets[0]: board "/Users/me/obsidian/board/" is already the board of the top-level fleet ("obsidian")`,
		},
		{
			"a fleet named like the named top-level fleet",
			operatorShape + "name: основной\nfleets:\n    - name: основной\n      board:\n        path: /x/board\n",
			`fleets[0]: name "основной" is already the name of the top-level fleet`,
		},
		{
			"a top-level name with spaces around it",
			operatorShape + "name: ' main'\n",
			`the top-level fleet: name " main" has leading or trailing spaces`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadText(t, tc.content)
			if err == nil {
				t.Fatalf("Load accepted %s", tc.name)
			}
			// The whole end of the message, so a hint that belongs to one
			// mistake cannot ride along on another.
			if !strings.HasSuffix(err.Error(), tc.want) {
				t.Fatalf("error\n%v\ndoes not end with\n%s", err, tc.want)
			}
		})
	}
}

func TestSaveThenLoadKeepsFleets(t *testing.T) {
	c, err := loadText(t, twoFleetShape)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := filepath.Join(t.TempDir(), "again.yaml")
	if err := Save(p, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	again, err := Load(p)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if !reflect.DeepEqual(again.FleetList(), c.FleetList()) {
		t.Fatalf("round trip changed fleets:\n%+v\nwant\n%+v", again.FleetList(), c.FleetList())
	}
}

func TestSaveRefusesFleetsLoadWouldRefuse(t *testing.T) {
	c := Default()
	c.Fleets = []fleet.Fleet{{Name: "a"}}
	if err := Save(filepath.Join(t.TempDir(), "c.yaml"), c); err == nil {
		t.Fatal("Save wrote a fleet with no board, which the next Load would refuse")
	}
}

func TestRemoveFleetRemovesOnlyTheNamedListedFleet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	cfg.BoardPath = filepath.Join(t.TempDir(), "main", "board")
	cfg.Fleets = []fleet.Fleet{
		{Name: "one", BoardPath: filepath.Join(t.TempDir(), "one", "board")},
		{Name: "two", BoardPath: filepath.Join(t.TempDir(), "two", "board")},
	}
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveFleet(p, "one")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Name != "one" || len(got.Fleets) != 1 || got.Fleets[0].Name != "two" {
		t.Fatalf("removed %+v, fleets after removal %+v", removed, got.Fleets)
	}
}

// The file is the operator's: removing one entry leaves every other line,
// comments included, as it was.
func TestRemoveFleetPreservesTheRestOfTheFile(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "config.yaml")
	raw := "# operator comment\nboard:\n  path: /main/board\nfleets:\n  - name: one\n    board:\n      path: /one/board\n  - name: two # keep this comment\n    board:\n      path: /two/board\nserver:\n  port: 7777 # hand tuned\n"
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveFleet(p, "one"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "# operator comment\nboard:\n  path: /main/board\nfleets:\n  - name: two # keep this comment\n    board:\n      path: /two/board\nserver:\n  port: 7777 # hand tuned\n"
	if string(got) != want {
		t.Fatalf("config after removal:\n%s\nwant:\n%s", got, want)
	}
}

// The first fleet is the configuration's top-level keys, not an entry in the
// list, and a name the list does not hold is refused rather than ignored.
func TestRemoveFleetRefusesTheFirstFleetAndAnUnknownOne(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "config.yaml")
	raw := "name: main\nboard:\n  path: /main/board\nfleets:\n  - name: one\n    board:\n      path: /one/board\n"
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main", "nobody"} {
		if _, err := RemoveFleet(p, name); err == nil {
			t.Errorf("RemoveFleet(%q) succeeded", name)
		}
	}
	if got, _ := os.ReadFile(p); string(got) != raw {
		t.Fatalf("a refused removal changed the file:\n%s", got)
	}
}
