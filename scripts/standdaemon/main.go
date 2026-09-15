// Command standdaemon is the fleet daemon of the window's CI stand with content
// (scripts/ci-window-stand.sh). It lays out a board and a job store chosen to
// show what broke for the operator in v0.10.0 -- session names longer than the
// sessions panel, more sessions than fit in it, one waiting for an answer, one
// stopped, and an orchestrator terminal with long wrapped lines and a status
// line -- and answers the stand's panel on its socket through
// internal/daemon/daemontest, the same stand-in the tests use, until it is
// stopped or the stand that started it goes.
//
// Usage: standdaemon -socket <path> -home <stand HOME> -board <board directory>
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon/daemontest"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// orchestratorShort is the session the stand's fleet names as its
// orchestrator (orchestrator.session in the stand's configuration, which
// scripts/ci-window-stand.sh writes): its attach shows the terminal's screen.
const orchestratorShort = "0c7e1a2b"

// stoppedShort is a session in the job store and not in the daemon's list:
// the panel shows it as stopped.
const stoppedShort = "5e55a0ff"

// silentShort is a session left unanswered inside AskUserQuestion: its
// transcript's last word is that call, 17 minutes old, and the panel names it
// in the row's badge, the widest a row gets.
const silentShort = "5e55a001"

type session struct {
	Short, Name, State, Tempo, Needs, Detail string
	// Unreported: the record carries no needs at all, a source that never
	// says whether anyone waits. The row's badge then takes the room its
	// name would have.
	Unreported bool
}

// sessions is what the daemon lists: more than the sessions panel shows at
// once, with names longer than it is wide, and one waiting for an answer.
var sessions = []session{
	{Short: orchestratorShort, Name: "fleetdeck orchestrator", State: "working"},
	{Short: "5e55a001", Name: "fleetdeck: Liquid Glass window with native panels over the board, the v0.10.1 fixes", State: "working", Detail: "running the window's tests on macOS 26"},
	{Short: "5e55a002", Name: "cruises: full review of the booking branch before the release candidate goes out", State: "idle", Tempo: "blocked",
		Needs: "answer: merge the migration first or after the API change? (first · after)", Detail: "merge the migration first or after the API change?"},
	{Short: "5e55a003", Name: "BS-27572: rewrite the payment reconciliation job so that it survives a restart halfway", State: "working"},
	{Short: "5e55a004", Name: "a parser for a municipal site whose pagination nobody ever documented", State: "idle", Tempo: "active", Unreported: true},
	{Short: "5e55a005", Name: "yandex-cloud-toolkit: move into a repository of its own and keep the whole history", State: "working"},
	{Short: "5e55a006", Name: "home network: check the router, the tunnel and the DNS after last night's outage", State: "working"},
	{Short: "5e55a007", Name: "docs: translate the engineering notes on the window and keep the headings in step", State: "idle", Tempo: "active"},
}

type card struct {
	File, ID, Zone, Stage string
	Progress              int
	Session, Title        string
}

// cards is the board: every stage holds something, the titles are long, and
// done is taller than the stand's window, as the operator's is (finished).
var cards = append([]card{
	{File: "T-101.md", ID: "T-101", Zone: "planned", Stage: "new", Title: "Measure how long the panel takes to answer under a loaded machine before choosing its deadline"},
	{File: "T-102.md", ID: "T-102", Zone: "urgent", Stage: "active", Progress: 60, Session: "5e55a001", Title: "fleetdeck: Liquid Glass window with native panels over the board, the v0.10.1 fixes"},
	{File: "T-103.md", ID: "T-103", Zone: "unplanned", Stage: "active", Progress: 20, Session: "5e55a002", Title: "cruises: full review of the booking branch before the release candidate goes out"},
	{File: "T-104.md", ID: "T-104", Zone: "planned", Stage: "review", Progress: 80, Session: "5e55a003", Title: "BS-27572: rewrite the payment reconciliation job so that it survives a restart halfway"},
	{File: "T-105.md", ID: "T-105", Zone: "niceToHave", Stage: "blocked", Progress: 40, Session: stoppedShort, Title: "fleetdeck: release v0.10.0 and hand the checklist to the operator"},
	{File: "T-106.md", ID: "T-106", Zone: "planned", Stage: "done", Progress: 100, Title: "Keep every branch after a merge: the operator's word on deleting them"},
}, finished(24)...)

// finished is n more done cards, numbered from T-201: a column taller than the
// stand's window (column_test.go).
func finished(n int) []card {
	out := make([]card, 0, n)
	for i := range n {
		id := fmt.Sprintf("T-%d", 201+i)
		out = append(out, card{File: id + ".md", ID: id, Zone: "planned", Stage: "done", Progress: 100,
			Title: fmt.Sprintf("Finished work number %d, kept on the board with a title long enough to wrap", i+1)})
	}
	return out
}

// layout writes the board's cards under board and the stopped session's record
// in home's job store.
func layout(home, board string) error {
	dir := filepath.Join(board, "cards")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, c := range cards {
		body := fmt.Sprintf("---\nid: %s\nzone: %s\nstage: %s\nprogress: %d\nsession: %q\nrepo: stand/fleet\ncreated: 2026-09-14\n---\n\n# %s\n",
			c.ID, c.Zone, c.Stage, c.Progress, c.Session, c.Title)
		if err := os.WriteFile(filepath.Join(dir, c.File), []byte(body), 0o644); err != nil {
			return err
		}
	}
	// The orchestrator's working order, as its wizard writes it, and the
	// daemon's control key, both only in the stand's own HOME and board: the
	// orchestrator panel shows neither the missing brief's warning nor a
	// read-only terminal, which the operator's panel does not.
	brief, err := orchestrator.Brief("en", orchestrator.Paths{Board: board})
	if err != nil {
		return err
	}
	if err := os.WriteFile(orchestrator.BriefPath(orchestrator.Paths{Board: board}), brief, 0o644); err != nil {
		return err
	}
	if err := writeSilentTranscript(home); err != nil {
		return err
	}
	keyDir := filepath.Join(home, ".claude", "daemon")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(keyDir, "control.key"), []byte("stand-control-key\n"), 0o600); err != nil {
		return err
	}
	stopped := filepath.Join(home, ".claude", "jobs", stoppedShort)
	if err := os.MkdirAll(stopped, 0o700); err != nil {
		return err
	}
	record, err := json.Marshal(map[string]string{
		"sessionId": "5e55a0ff-0000-4000-8000-000000000000",
		"name":      "fleetdeck: release v0.10.0 and hand the checklist to the operator",
		"cwd":       home,
		"state":     "done",
		"detail":    "released v0.10.0; the checklist is with the operator",
		"updatedAt": "2026-09-14T12:23:00Z",
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stopped, "state.json"), record, 0o600)
}

// sessionID is the i-th session's transcript UUID, the name its transcript is
// found by.
func sessionID(i int) string {
	return fmt.Sprintf("%s-0000-4000-8000-%012d", sessions[i].Short, i)
}

// writeSilentTranscript writes, under home's projects, the transcript of the
// session left unanswered: one call to AskUserQuestion that never came back.
func writeSilentTranscript(home string) error {
	for i, s := range sessions {
		if s.Short != silentShort {
			continue
		}
		line, err := json.Marshal(map[string]any{
			"type":      "assistant",
			"timestamp": time.Now().Add(-17 * time.Minute).UTC().Format(time.RFC3339),
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]any{{"type": "tool_use", "id": "call-1", "name": "AskUserQuestion", "input": map[string]any{}}},
			},
		})
		if err != nil {
			return err
		}
		dir := filepath.Join(home, ".claude", "projects", "stand")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, sessionID(i)+".jsonl"), append(line, '\n'), 0o600)
	}
	return fmt.Errorf("no session %s to leave unanswered", silentShort)
}

// listRecords is the list reply's records (docs/protocol/daemon-control-socket.md,
// section 4), cwd being where every session runs.
func listRecords(cwd string) (string, error) {
	records := make([]string, 0, len(sessions))
	for i, s := range sessions {
		record := map[string]any{
			"short":     s.Short,
			"sessionId": sessionID(i),
			"name":      s.Name,
			"state":     s.State,
			"tempo":     s.Tempo,
			"needs":     s.Needs,
			"detail":    s.Detail,
			"cwd":       cwd,
			"source":    "shell",
		}
		if s.Unreported {
			delete(record, "needs")
		}
		r, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		records = append(records, string(r))
	}
	return strings.Join(records, ","), nil
}

// screen is the orchestrator's terminal as Claude Code draws it: lines longer
// than the orchestrator panel is wide, and a status line on the last row.
func screen() []byte {
	lines := []string{
		"\x1b[2J\x1b[H",
		"\x1b[1m⏺\x1b[0m Read the stand's board and every session the daemon lists, then compare each card's session with the list, so that a card whose session has stopped reads as stopped before the operator opens it.\r\n\r\n",
		"\x1b[1m⏺\x1b[0m Update(web/app.css)\r\n",
		"  ⎿  Updated web/app.css with 14 additions and 3 removals: the board keeps its right inset at zero under the sessions glass, and a sheet keeps clear of that panel by an inset of its own.\r\n\r\n",
		"\x1b[1m⏺\x1b[0m Bash(go test -race -count=1 ./cmd/fleetdeck-window/ ./internal/supervisor/ ./internal/daemon/...)\r\n",
		"  ⎿  ok  github.com/kroticw/fleetdeck/cmd/fleetdeck-window  21.610s\r\n",
		"     ok  github.com/kroticw/fleetdeck/internal/supervisor  67.981s\r\n\r\n",
		"\x1b[999;1H\x1b[7m  ⏵⏵ accept edits on (shift+tab to cycle) · 42% context left · fleetdeck orchestrator on the stand  \x1b[0m",
	}
	return []byte(strings.Join(lines, ""))
}

func handlers(cwd string, hold <-chan struct{}) (daemontest.Handler, error) {
	list, err := listRecords(cwd)
	if err != nil {
		return nil, err
	}
	return daemontest.Ops(map[string]daemontest.Handler{
		"list":   daemontest.List(list),
		"attach": daemontest.Screen(screen(), hold),
		"resize": daemontest.Resized,
	}), nil
}

func main() {
	socket := flag.String("socket", "", "the stand's daemon socket, which the stand's window hands its panel")
	home := flag.String("home", "", "the stand's HOME, whose job store gets the stopped session")
	board := flag.String("board", "", "the stand's board directory")
	flag.Parse()
	if *socket == "" || *home == "" || *board == "" {
		log.Fatal("standdaemon: -socket, -home and -board are all needed")
	}
	if err := layout(*home, *board); err != nil {
		log.Fatalf("standdaemon: lay out the stand: %v", err)
	}
	hold := make(chan struct{})
	handle, err := handlers(*home, hold)
	if err != nil {
		log.Fatalf("standdaemon: %v", err)
	}
	d, err := daemontest.Start(*socket, handle)
	if err != nil {
		log.Fatalf("standdaemon: listen on %s: %v", *socket, err)
	}
	log.Printf("standdaemon: %d sessions, %d cards and one stopped session, on %s", len(sessions), len(cards), d.Socket)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	parent := os.Getppid()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			close(hold)
			_ = d.Close()
			return
		case <-tick.C:
			// The stand that started this one is gone: nothing is left to answer.
			if os.Getppid() != parent {
				close(hold)
				_ = d.Close()
				return
			}
		}
	}
}
