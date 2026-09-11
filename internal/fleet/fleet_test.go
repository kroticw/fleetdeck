package fleet

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
)

func TestDefaultNameIsTheFolderAboveTheBoard(t *testing.T) {
	cases := map[string]string{
		"/Users/me/fleetdeck/board":        "fleetdeck",
		"/Users/me/obsidian/board/":        "obsidian",
		"/Users/me/obsidian/./board":       "obsidian",
		"":                                 "main",
		"/board":                           "main",
		"/Users/me/clining-fleet/my-board": "clining-fleet",
		"/Users/me/ padded/board":          "main",
		"/Users/me/tab\tin/board":          "main",
	}
	for boardPath, want := range cases {
		if got := DefaultName(boardPath); got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", boardPath, got, want)
		}
	}
}

func twoFleets() []Fleet {
	return []Fleet{
		{Name: "obsidian", BoardPath: "/Users/me/obsidian/board", Orchestrator: "06a1f607"},
		{Name: "clining", BoardPath: "/Users/me/clining/board", Orchestrator: "1a2b3c4d"},
	}
}

func TestValidateAcceptsDistinctFleets(t *testing.T) {
	if err := Validate(twoFleets()); err != nil {
		t.Fatalf("two distinct fleets must be valid: %v", err)
	}
}

func TestValidateAcceptsTheFirstFleetWithoutABoard(t *testing.T) {
	// A panel with no board is a session manager (spec section 11); that is
	// the first fleet as every existing configuration without board.path has it.
	if err := Validate([]Fleet{{Name: "main"}}); err != nil {
		t.Fatalf("a lone first fleet without a board must be valid: %v", err)
	}
}

func TestValidateAcceptsFleetsWithNoOrchestratorYet(t *testing.T) {
	fleets := twoFleets()
	fleets[0].Orchestrator, fleets[1].Orchestrator = "", ""
	if err := Validate(fleets); err != nil {
		t.Fatalf("two fleets with no orchestrator pinned are not a conflict: %v", err)
	}
}

func TestValidateRefusesBrokenFleets(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func([]Fleet) []Fleet
		wantIndex int
		wantOther int
		wantField string
		wantText  string
	}{
		{"empty name", func(f []Fleet) []Fleet { f[1].Name = ""; return f }, 1, -1, "", "fleet 1: name must not be empty"},
		{"blank name", func(f []Fleet) []Fleet { f[1].Name = "   "; return f }, 1, -1, "", "name must not be empty"},
		{"spaces around name", func(f []Fleet) []Fleet { f[1].Name = " clining"; return f }, 1, -1, "", "leading or trailing spaces"},
		{"control character in name", func(f []Fleet) []Fleet { f[1].Name = "cli\nning"; return f }, 1, -1, "", "control character"},
		{"same name twice", func(f []Fleet) []Fleet { f[1].Name = "obsidian"; return f }, 1, 0, "name", `fleet 1: name "obsidian" is already the name of fleet 0`},
		{"same orchestrator twice", func(f []Fleet) []Fleet { f[1].Orchestrator = "06a1f607"; return f }, 1, 0, "orchestrator", `orchestrator "06a1f607" is already the orchestrator of fleet 0 ("obsidian")`},
		{"same board twice", func(f []Fleet) []Fleet { f[1].BoardPath = "/Users/me/obsidian/board/"; return f }, 1, 0, "board", `board "/Users/me/obsidian/board/" is already the board of fleet 0 ("obsidian")`},
		{"a later fleet without a board", func(f []Fleet) []Fleet { f[1].BoardPath = ""; return f }, 1, -1, "", "board path must be set"},
		{"no fleet at all", func([]Fleet) []Fleet { return nil }, -1, -1, "", "no fleet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.mutate(twoFleets()))
			if err == nil {
				t.Fatalf("Validate accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("error %q does not say %q", err, tc.wantText)
			}
			var fe *Error
			if !errors.As(err, &fe) {
				t.Fatalf("error %q is not a *fleet.Error, so the caller cannot say where in its file it is", err)
			}
			if fe.Index != tc.wantIndex || fe.Other != tc.wantOther || fe.Field != tc.wantField {
				t.Fatalf("error %q is {Index %d, Other %d, Field %q}, want {%d, %d, %q}",
					err, fe.Index, fe.Other, fe.Field, tc.wantIndex, tc.wantOther, tc.wantField)
			}
		})
	}
}

func TestAnEmptyListIsNamedAsSuchAndNotAsAFleet(t *testing.T) {
	if got, want := Validate(nil).Error(), "no fleet is configured"; got != want {
		t.Fatalf("Validate(nil) = %q, want %q", got, want)
	}
}

func TestDescribeNamesPositionsTheCallersWay(t *testing.T) {
	fleets := twoFleets()
	fleets[1].Name = "obsidian"
	var fe *Error
	if !errors.As(Validate(fleets), &fe) {
		t.Fatal("expected a *fleet.Error")
	}
	where := func(i int) string { return fmt.Sprintf("entry #%d", i+10) }
	if got, want := fe.Describe(where), `entry #11: name "obsidian" is already the name of entry #10`; got != want {
		t.Fatalf("Describe = %q, want %q", got, want)
	}
}

func TestSelectPicksByExactName(t *testing.T) {
	fleets := twoFleets()
	got, err := Select(fleets, "clining")
	if err != nil {
		t.Fatalf("Select(clining): %v", err)
	}
	if got.Orchestrator != "1a2b3c4d" {
		t.Fatalf("Select(clining) returned %+v", got)
	}
}

func TestSelectWithNoNameIsTheFirstFleet(t *testing.T) {
	got, err := Select(twoFleets(), "")
	if err != nil {
		t.Fatalf("Select(\"\"): %v", err)
	}
	if got.Name != "obsidian" {
		t.Fatalf("a tab that names no fleet must get the first one, got %q", got.Name)
	}
}

func TestSelectRefusesAnUnknownNameInsteadOfFallingBack(t *testing.T) {
	// A tab left on a fleet that was renamed must not be quietly shown another
	// fleet's board under the old name in its address bar.
	for _, name := range []string{"Clining", "gone", " clining"} {
		_, err := Select(twoFleets(), name)
		if err == nil {
			t.Fatalf("Select(%q) fell back to some fleet instead of refusing", name)
		}
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("Select(%q) error %v is not ErrUnknown", name, err)
		}
		if !strings.Contains(err.Error(), `"obsidian", "clining"`) {
			t.Fatalf("Select(%q) error %q does not list the fleets there are", name, err)
		}
	}
}

func TestClaimsComeFromOrchestratorsAndCards(t *testing.T) {
	fleets := twoFleets()
	cards := map[string][]board.Card{
		"obsidian": {
			{Path: "/o/cards/a.md", Session: "aaaa1111"},
			{Path: "/o/cards/b.md", Session: ""},
			{Path: "/o/cards/c.md", Session: "06a1f607"},
		},
		"clining": {
			{Path: "/c/cards/d.md", Session: "bbbb2222"},
			{Path: "/c/cards/e.md", Session: "aaaa1111"},
		},
	}
	got := Claims(fleets, cards)
	want := map[string][]string{
		"06a1f607": {"obsidian"},
		"1a2b3c4d": {"clining"},
		"aaaa1111": {"obsidian", "clining"},
		"bbbb2222": {"clining"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Claims =\n%v\nwant\n%v", got, want)
	}
}

func TestClaimsListFleetsInConfigurationOrder(t *testing.T) {
	fleets := twoFleets()
	fleets[0], fleets[1] = fleets[1], fleets[0]
	cards := map[string][]board.Card{
		"obsidian": {{Path: "/o/cards/a.md", Session: "aaaa1111"}},
		"clining":  {{Path: "/c/cards/e.md", Session: "aaaa1111"}},
	}
	if got := Claims(fleets, cards)["aaaa1111"]; !reflect.DeepEqual(got, []string{"clining", "obsidian"}) {
		t.Fatalf("a session two fleets claim must list them in configuration order, got %v", got)
	}
}

func TestClaimsIgnoreCardsOfAFleetThatIsNotConfigured(t *testing.T) {
	cards := map[string][]board.Card{"gone": {{Path: "/g/cards/a.md", Session: "aaaa1111"}}}
	if got := Claims(twoFleets(), cards); len(got["aaaa1111"]) != 0 {
		t.Fatalf("cards keyed by a fleet that is not configured claimed %v", got["aaaa1111"])
	}
}
