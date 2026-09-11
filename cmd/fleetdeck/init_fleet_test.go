package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/fleet"
)

// firstFleet runs the plain init a first launch runs and returns the home and
// the configuration file it made.
func firstFleet(t *testing.T) (home, cfgPath string) {
	t.Helper()
	home = t.TempDir()
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), out: io.Discard}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	return home, filepath.Join(home, ".config", "fleetdeck", "config.yaml")
}

func TestInitFleetAddsASecondFleetWithItsOwnWorkspace(t *testing.T) {
	home, cfgPath := firstFleet(t)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "clining-fleet")
	var out bytes.Buffer
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), workspace: root, fleet: "clining", out: &out}); err != nil {
		t.Fatalf("init --fleet: %v\n%s", err, out.String())
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("init --fleet left a configuration that does not load: %v", err)
	}
	fleets := cfg.FleetList()
	want := fleet.Fleet{Name: "clining", BoardPath: filepath.Join(root, "board"), DocsPaths: []string{filepath.Join(root, "docs")}}
	if len(fleets) != 2 || fleets[0].Name != "fleetdeck" || !slices.Equal(fleets[1].DocsPaths, want.DocsPaths) || fleets[1].BoardPath != want.BoardPath || fleets[1].Name != want.Name {
		t.Fatalf("fleets after init --fleet = %+v", fleets)
	}
	if _, err := board.Scan(want.BoardPath); err != nil {
		t.Fatalf("the new fleet's board must scan: %v", err)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Fatalf("the first fleet's configuration was rewritten, not added to:\n%s", after)
	}

	perms, _ := readSettings(t, settingsPathOf(home))["permissions"].(map[string]any)
	if dirs := anyStrings(perms["additionalDirectories"]); !slices.Contains(dirs, root) {
		t.Fatalf("agents are not let into the new fleet's workspace: %q", dirs)
	}
	for _, say := range []string{"clining", root, "restart"} {
		if !strings.Contains(out.String(), say) {
			t.Fatalf("init --fleet does not say %q:\n%s", say, out.String())
		}
	}
}

func TestInitFleetWithABoardAlone(t *testing.T) {
	home, cfgPath := firstFleet(t)
	dir := filepath.Join(t.TempDir(), "work-board")
	if err := runInit(initEnv{home: home, binary: fakeInstall(t, true), board: dir, fleet: "work", out: io.Discard}); err != nil {
		t.Fatalf("init --fleet --board: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.FleetList()[1]
	if got.Name != "work" || got.BoardPath != dir || len(got.DocsPaths) != 0 {
		t.Fatalf("board-only fleet = %+v", got)
	}
}

func TestInitFleetRunTwiceKeepsTheFleet(t *testing.T) {
	home, cfgPath := firstFleet(t)
	root := filepath.Join(t.TempDir(), "clining-fleet")
	env := initEnv{home: home, binary: fakeInstall(t, true), workspace: root, fleet: "clining", out: io.Discard}
	if err := runInit(env); err != nil {
		t.Fatal(err)
	}
	once, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	env.out = &out
	if err := runInit(env); err != nil {
		t.Fatalf("a second identical init --fleet must succeed: %v\n%s", err, out.String())
	}
	twice, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("a second identical init --fleet changed the configuration:\n%s", twice)
	}
	if !strings.Contains(out.String(), "kept") {
		t.Fatalf("a kept fleet must be said to be kept:\n%s", out.String())
	}
}

func TestInitFleetAddsNothingWhenItsBoardCannotBeMade(t *testing.T) {
	// A fleet in the configuration whose board does not exist would point the
	// panel at nothing, the same rule a first run keeps for its own board.
	home, cfgPath := firstFleet(t)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = runInit(initEnv{home: home, binary: fakeInstall(t, true), workspace: filepath.Join(file, "fleet"), fleet: "clining", out: &out})
	if err == nil {
		t.Fatalf("init --fleet over a board it could not make succeeded:\n%s", out.String())
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("a fleet whose board could not be made was added:\n%s", after)
	}
	if !strings.Contains(out.String(), "fleet clining not added") {
		t.Fatalf("init --fleet does not say the fleet was not added:\n%s", out.String())
	}
}

func TestInitFleetRefusesBeforeMakingAnything(t *testing.T) {
	cases := []struct {
		name  string
		fresh bool
		fleet string
		place string // "workspace", "board" or ""
		want  string
	}{
		{"a fresh machine", true, "clining", "workspace", "the first fleet"},
		{"no place for the board", false, "clining", "", "--fleet needs --workspace or --board"},
		{"the first fleet's name", false, "fleetdeck", "workspace", `name "fleetdeck" is already the name of the top-level fleet`},
		{"a name with spaces around it", false, " clining", "workspace", "leading or trailing spaces"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var home, cfgPath string
			if tc.fresh {
				home = t.TempDir()
				cfgPath = filepath.Join(home, ".config", "fleetdeck", "config.yaml")
			} else {
				home, cfgPath = firstFleet(t)
			}
			before, _ := os.ReadFile(cfgPath)
			root := filepath.Join(t.TempDir(), "new-fleet")
			env := initEnv{home: home, binary: fakeInstall(t, true), fleet: tc.fleet, out: io.Discard}
			switch tc.place {
			case "workspace":
				env.workspace = root
			case "board":
				env.board = root
			}
			err := runInit(env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("init --fleet error = %v, want one saying %q", err, tc.want)
			}
			if _, statErr := os.Stat(root); statErr == nil {
				t.Fatal("a refused init --fleet made the new fleet's folder anyway")
			}
			after, _ := os.ReadFile(cfgPath)
			if !bytes.Equal(before, after) {
				t.Fatalf("a refused init --fleet changed the configuration:\n%s", after)
			}
		})
	}
}
