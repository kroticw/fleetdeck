package web

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
)

// fixturePath is the snapshot the frontend's own tests render from. It lives
// under web/tests/, deliberately outside every go:embed pattern in this package,
// so no test data can reach the binary or the HTTP surface.
var fixturePath = filepath.Join("tests", "testdata", "snapshot.json")

// updateEnv regenerates the fixture instead of comparing against it. Set it when
// a field is legitimately added to or renamed in a snapshot, then read the diff.
const updateEnv = "UPDATE_SNAPSHOT_FIXTURE"

const cardBody = "# Fleet UI <panel> \"v2\"\n" +
	"\n" +
	"Depends on [[card-keeping]] and on [[nowhere]].\n" +
	"\n" +
	"- **stage** and progress are the two fields the panel writes\n" +
	"- everything else on this card belongs to the agent\n" +
	"\n" +
	"An agent can write anything here, including <img src=x onerror=alert(1)>.\n" +
	"\n" +
	"```sh\n" +
	"echo \"<script>alert(1)</script>\"\n" +
	"```\n"

// snapshotFixture builds a snapshot out of the real structs, so every JSON name
// in the golden file comes from a struct tag rather than from something typed
// twice. A field renamed in internal/board or internal/state fails this test
// instead of quietly rendering an empty panel in the browser.
func snapshotFixture() state.Snapshot {
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	cost := 1.25
	return state.Snapshot{
		Sessions: []state.SessionView{
			{
				Session: daemon.Session{
					Short:      "a1b2c3",
					Nonce:      "n1",
					SessionID:  "6f1c9a52-0f4e-4a0b-9d33-2f0c1f5a77bd",
					PID:        4242,
					Attempt:    1,
					StartedAt:  at.Add(-time.Hour).UnixMilli(),
					CreatedAt:  at.Add(-time.Hour).UnixMilli(),
					CWD:        "/repo/fleetdeck",
					Backend:    "claude",
					Tempo:      "active",
					State:      "working",
					Detail:     "writing the card panel",
					Intent:     "fleetdeck ui",
					Name:       "ui5",
					Agent:      "claude",
					CLIVersion: "2.1.263",
					Source:     "daemon",
				},
				Context:   &transcript.Usage{Tokens: 120000, Window: 1000000},
				CardPath:  "/board/fleet-ui.md",
				SilentFor: 90 * time.Second,
				Model:     "Claude Opus 5",
				CostUSD:   &cost,
			},
		},
		Cards: []board.Card{
			{
				Path:     "/board/fleet-ui.md",
				Zone:     "planned",
				Stage:    "active",
				Progress: 40,
				Session:  "a1b2c3",
				Repo:     "fleetdeck",
				Created:  "2026-09-01",
				Title:    "Fleet UI <panel> \"v2\"",
				Body:     cardBody,
				Links:    []string{"card-keeping", "nowhere"},
			},
			{
				// Names a session the daemon no longer lists, which is why its
				// path is in OrphanCards below. It also links back to the card
				// above, which is what the panel's backlink list renders.
				Path:     "/board/card-keeping.md",
				Zone:     "niceToHave",
				Stage:    "review",
				Progress: 80,
				Session:  "dead99",
				Repo:     "fleetdeck",
				Created:  "2026-08-20",
				Title:    "Card keeping",
				Body:     "# Card keeping\n\nSee [[fleet-ui]] for the panel.\n",
				Links:    []string{"fleet-ui"},
			},
			{
				// A card that does not parse. The board still carries it, and the
				// panel has to show it as unwritable rather than hide it.
				Path:       "/board/broken.md",
				ParseError: "no frontmatter block",
			},
		},
		Limits: &usage.Limits{
			FiveHour:  usage.Window{Utilization: 0.42, ResetsAt: at.Add(3 * time.Hour)},
			SevenDay:  usage.Window{Utilization: 0.11, ResetsAt: at.Add(96 * time.Hour)},
			FetchedAt: at,
		},
		OrphanCards: []string{"/board/card-keeping.md"},
		At:          at,
	}
}

func TestSnapshotFixtureMatchesStructs(t *testing.T) {
	got, err := json.MarshalIndent(snapshotFixture(), "", "  ")
	if err != nil {
		t.Fatalf("marshal the fixture snapshot: %v", err)
	}
	got = append(got, '\n')

	if os.Getenv(updateEnv) != "" {
		if err := os.WriteFile(fixturePath, got, 0o644); err != nil {
			t.Fatalf("rewrite %s: %v", fixturePath, err)
		}
		t.Fatalf("%s rewrote %s; unset %s and review the diff", updateEnv, fixturePath, updateEnv)
	}

	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate it with %s=1 go test ./web)", fixturePath, err, updateEnv)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s no longer matches what state.Snapshot marshals to.\n"+
			"The frontend tests render from this file, so a field renamed in\n"+
			"internal/board or internal/state has to reach web/js as well.\n"+
			"Regenerate with %s=1 go test ./web and review the diff.\n\n"+
			"marshalled:\n%s\nfixture:\n%s", fixturePath, updateEnv, got, want)
	}
}

// TestEmbedExcludesTestData is the other half of that arrangement: the fixture
// and the frontend tests must stay out of the binary. go:embed patterns are easy
// to widen by accident — an "all:" prefix or a bare "." would sweep both in — and
// nothing else in the build would notice.
func TestEmbedExcludesTestData(t *testing.T) {
	for _, name := range []string{"tests", "tests/testdata/snapshot.json", "package.json"} {
		if _, err := fs.Stat(FS, name); err == nil {
			t.Errorf("%s is embedded in web.FS and is reachable over HTTP; it must not be", name)
		}
	}
}
