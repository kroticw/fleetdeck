package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/fleet"
)

func writeText(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func readText(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var clining = fleet.Fleet{
	Name:         "clining",
	BoardPath:    "/Users/me/clining/board",
	DocsPaths:    []string{"/Users/me/clining/docs"},
	Orchestrator: "1a2b3c4d",
}

const cliningEntry = `    - name: clining
      board:
        path: /Users/me/clining/board
      docs:
        paths:
            - /Users/me/clining/docs
      orchestrator:
        session: 1a2b3c4d
`

// A hand-written configuration with comments where an operator puts them.
const commentedShape = `# my fleetdeck settings
board:
  path: /Users/me/obsidian/board   # the old board, kept
docs:
  paths:
    - /Users/me/obsidian/board/docs
orchestrator:
  session: 06a1f607
server:
  port: 7777  # the statusline looks here
`

func TestAddFleetAppendsAListToAFileWithoutOne(t *testing.T) {
	p := writeText(t, commentedShape)
	if err := AddFleet(p, clining); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	if got, want := readText(t, p), commentedShape+"fleets:\n"+cliningEntry; got != want {
		t.Fatalf("file after AddFleet:\n%s\nwant\n%s", got, want)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load after AddFleet: %v", err)
	}
	if got := c.FleetList(); len(got) != 2 || !reflect.DeepEqual(got[1], clining) {
		t.Fatalf("FleetList after AddFleet = %+v", got)
	}
}

func TestAddFleetToAFileWithNoTrailingNewline(t *testing.T) {
	p := writeText(t, strings.TrimSuffix(commentedShape, "\n"))
	if err := AddFleet(p, clining); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	if got, want := readText(t, p), commentedShape+"fleets:\n"+cliningEntry; got != want {
		t.Fatalf("file after AddFleet:\n%s\nwant\n%s", got, want)
	}
}

func TestAddFleetAppendsToTheEndOfAnExistingList(t *testing.T) {
	before := "board:\n    path: /Users/me/obsidian/board\n" +
		"fleets:\n" +
		"    - name: work   # the day job\n" +
		"      board:\n" +
		"        path: /Users/me/work/board\n" +
		"\n" +
		"# the statusline looks here\n" +
		"server:\n" +
		"    port: 7777\n"
	want := "board:\n    path: /Users/me/obsidian/board\n" +
		"fleets:\n" +
		"    - name: work   # the day job\n" +
		"      board:\n" +
		"        path: /Users/me/work/board\n" +
		cliningEntry +
		"\n" +
		"# the statusline looks here\n" +
		"server:\n" +
		"    port: 7777\n"
	p := writeText(t, before)
	if err := AddFleet(p, clining); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	if got := readText(t, p); got != want {
		t.Fatalf("file after AddFleet:\n%s\nwant\n%s", got, want)
	}
}

func TestAddFleetFollowsAListWrittenAtColumnZero(t *testing.T) {
	before := "board:\n  path: /Users/me/obsidian/board\n" +
		"fleets:\n" +
		"- name: work\n" +
		"  board:\n" +
		"    path: /Users/me/work/board\n"
	p := writeText(t, before)
	if err := AddFleet(p, clining); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	got := readText(t, p)
	if !strings.HasPrefix(got, before+"- name: clining\n  board:\n") {
		t.Fatalf("the new entry does not follow the list's own indentation:\n%s", got)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load after AddFleet: %v", err)
	}
	if names := fleetNames(c); !reflect.DeepEqual(names, []string{"obsidian", "work", "clining"}) {
		t.Fatalf("fleets after AddFleet: %v", names)
	}
}

func TestAddFleetOpensAnEmptyFlowList(t *testing.T) {
	p := writeText(t, "board:\n    path: /Users/me/obsidian/board\nfleets: []  # none yet\n")
	if err := AddFleet(p, clining); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	want := "board:\n    path: /Users/me/obsidian/board\nfleets:  # none yet\n" + cliningEntry
	if got := readText(t, p); got != want {
		t.Fatalf("file after AddFleet:\n%s\nwant\n%s", got, want)
	}
}

func TestAddFleetRefusesAndLeavesTheFileAlone(t *testing.T) {
	cases := []struct {
		name  string
		fleet fleet.Fleet
		file  string
		want  string
	}{
		{"a name the top-level fleet has", fleet.Fleet{Name: "obsidian", BoardPath: "/x/board"}, commentedShape, `name "obsidian" is already the name of the top-level fleet`},
		{"no board", fleet.Fleet{Name: "clining"}, commentedShape, "board path must be set"},
		{"the top-level orchestrator", fleet.Fleet{Name: "clining", BoardPath: "/x/board", Orchestrator: "06a1f607"}, commentedShape, `orchestrator "06a1f607" is already the orchestrator of the top-level fleet`},
		{"a newline in a value", fleet.Fleet{Name: "clining", BoardPath: "/x/board\nserver: 1"}, commentedShape, "newline"},
		{"a list written inline", clining, "board:\n    path: /b\nfleets: [{name: work, board: {path: /w}}]\n", "fleets is not written as a block list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeText(t, tc.file)
			err := AddFleet(p, tc.fleet)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("AddFleet error = %v, want one saying %q", err, tc.want)
			}
			if got := readText(t, p); got != tc.file {
				t.Fatalf("a refused AddFleet changed the file:\n%s", got)
			}
		})
	}
}

func fleetNames(c Config) []string {
	var out []string
	for _, f := range c.FleetList() {
		out = append(out, f.Name)
	}
	return out
}

const twoEntryList = `board:
    path: /Users/me/obsidian/board
orchestrator:
    session: 06a1f607
fleets:
    - name: clining
      board:
        path: /Users/me/clining/board
      orchestrator:
        session: 1a2b3c4d   # pinned from the panel
    - board:
        path: /Users/me/work/board
      name: "work"
server:
    port: 7777
`

func TestSetFleetOrchestratorRewritesOnlyThatLine(t *testing.T) {
	p := writeText(t, twoEntryList)
	if err := SetFleetOrchestrator(p, "clining", "cafe0001"); err != nil {
		t.Fatalf("SetFleetOrchestrator: %v", err)
	}
	want := strings.Replace(twoEntryList, "session: 1a2b3c4d   # pinned", "session: cafe0001  # pinned", 1)
	if got := readText(t, p); got != want {
		t.Fatalf("file after SetFleetOrchestrator:\n%s\nwant\n%s", got, want)
	}
}

func TestSetFleetOrchestratorAddsTheKeyToAFleetWithout(t *testing.T) {
	p := writeText(t, twoEntryList)
	if err := SetFleetOrchestrator(p, "work", "cafe0002"); err != nil {
		t.Fatalf("SetFleetOrchestrator: %v", err)
	}
	want := strings.Replace(twoEntryList, "      name: \"work\"\n",
		"      name: \"work\"\n      orchestrator:\n        session: cafe0002\n", 1)
	if got := readText(t, p); got != want {
		t.Fatalf("file after SetFleetOrchestrator:\n%s\nwant\n%s", got, want)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.FleetList()[2].Orchestrator; got != "cafe0002" {
		t.Fatalf("work's orchestrator = %q", got)
	}
}

func TestSetFleetOrchestratorUnpins(t *testing.T) {
	p := writeText(t, twoEntryList)
	if err := SetFleetOrchestrator(p, "clining", ""); err != nil {
		t.Fatalf("SetFleetOrchestrator: %v", err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.FleetList()[1].Orchestrator; got != "" {
		t.Fatalf("clining's orchestrator after unpinning = %q", got)
	}
}

func TestSetFleetOrchestratorUnpinningAFleetWithoutOneChangesNothing(t *testing.T) {
	p := writeText(t, twoEntryList)
	if err := SetFleetOrchestrator(p, "work", ""); err != nil {
		t.Fatalf("SetFleetOrchestrator: %v", err)
	}
	if got := readText(t, p); got != twoEntryList {
		t.Fatalf("unpinning an unpinned fleet changed the file:\n%s", got)
	}
}

func TestAddFleetToAnEmptyFile(t *testing.T) {
	p := writeText(t, "")
	err := AddFleet(p, clining)
	// An empty file is the top-level fleet with no board, which is valid, so
	// the list opens on the first line.
	if err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	if got, want := readText(t, p), "fleets:\n"+cliningEntry; got != want {
		t.Fatalf("file after AddFleet:\n%s\nwant\n%s", got, want)
	}
}

func TestSetFleetOrchestratorFillsAnEmptyOrchestratorKey(t *testing.T) {
	before := "board:\n    path: /b\nfleets:\n    - name: work\n      board:\n        path: /w\n      orchestrator:   # later\n"
	p := writeText(t, before)
	if err := SetFleetOrchestrator(p, "work", "cafe0004"); err != nil {
		t.Fatalf("SetFleetOrchestrator: %v", err)
	}
	if got, want := readText(t, p), before+"        session: cafe0004\n"; got != want {
		t.Fatalf("file after SetFleetOrchestrator:\n%s\nwant\n%s", got, want)
	}
}

func TestSetFleetOrchestratorRefusesAnInlineOrchestrator(t *testing.T) {
	before := "board:\n    path: /b\nfleets:\n    - name: work\n      board: {path: /w}\n      orchestrator: {session: cafe0005}\n"
	p := writeText(t, before)
	err := SetFleetOrchestrator(p, "work", "cafe0006")
	if err == nil || !strings.Contains(err.Error(), "orchestrator is not written as a block") {
		t.Fatalf("SetFleetOrchestrator error = %v", err)
	}
	if got := readText(t, p); got != before {
		t.Fatalf("a refused write changed the file:\n%s", got)
	}
}

func TestSetFleetOrchestratorRefusesAndLeavesTheFileAlone(t *testing.T) {
	cases := []struct {
		name, fleetName, short, want string
	}{
		{"a fleet that is not listed", "gone", "cafe0003", `fleet "gone" is not in the fleets list`},
		{"the top-level fleet", "obsidian", "cafe0003", `fleet "obsidian" is not in the fleets list`},
		{"another fleet's orchestrator", "work", "1a2b3c4d", `is already the orchestrator of fleets[0]`},
		{"the top-level orchestrator", "work", "06a1f607", `is already the orchestrator of the top-level fleet`},
		{"a newline", "work", "cafe\nserver: 1", "newline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeText(t, twoEntryList)
			err := SetFleetOrchestrator(p, tc.fleetName, tc.short)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SetFleetOrchestrator error = %v, want one saying %q", err, tc.want)
			}
			if got := readText(t, p); got != twoEntryList {
				t.Fatalf("a refused SetFleetOrchestrator changed the file:\n%s", got)
			}
		})
	}
}
