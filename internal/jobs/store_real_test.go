package jobs

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These tests run against the real job store on the machine running them,
// and against the real `claude` CLI, rather than against a store invented in
// a temp directory.
//
// That is the point of them. Every other test in this package builds the
// store it then reads, so it can only ever prove that this package agrees
// with this package's own idea of the format — and the format is not this
// project's to define. This project has already paid once for a test suite
// that checked a real path only inside t.TempDir(): the code was green and
// the real path was wrong the whole time. A store this package never wrote
// is the only thing that can catch Claude Code moving the directory,
// renaming state.json, or renaming a field inside it.
//
// Both skip when there is nothing real to check — no store on this machine,
// no CLI on PATH — so a CI runner still runs the rest of the package. A skip
// is honest: it says the check did not happen, unlike a green test that
// checked a fixture.
//
// Both are read-only. Neither starts, stops or writes to a session.

// TestRealStoreParses reads the actual ~/.claude/jobs and requires the
// records to come out usable: the short id is the directory name, and a
// session that has a state.json has an id to be resumed by. A format change
// that empties the fields would leave the panel showing rows with no names
// and nothing resumable, which is what this catches.
func TestRealStoreParses(t *testing.T) {
	dir := realStore(t)

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(%s): %v", dir, err)
	}
	if len(records) == 0 {
		t.Skipf("job store %s holds no sessions; nothing real to check", dir)
	}

	withID := 0
	for _, r := range records {
		if r.Short == "" {
			t.Errorf("record with no short id: %+v", r)
		}
		if _, err := os.Stat(filepath.Join(dir, r.Short, stateFile)); err != nil {
			t.Errorf("record %s does not correspond to a %s on disk: %v", r.Short, stateFile, err)
		}
		if r.SessionID != "" {
			withID++
		}
	}
	if withID == 0 {
		t.Errorf("not one of %d real records carried a session id — the id field has probably been renamed", len(records))
	}
	t.Logf("read %d records from %s, %d with a session id", len(records), dir, withID)
}

// TestRealStoreAgreesWithTheAgentsView holds this package to the one claim
// it makes: that walking the store sees the same sessions the agents view
// does. `claude agents --json --all` walks the same store from inside the
// CLI, which makes it an oracle this package cannot cheat against — a
// renamed directory, a renamed file or a renamed field shows up here as a
// disagreement, not as a quietly shorter list.
//
// The store is read on both sides of the CLI call because a session can be
// started or stopped by someone else while this test runs: the CLI's answer
// has to lie between what was there before and what was there after, not
// match one instant exactly.
func TestRealStoreAgreesWithTheAgentsView(t *testing.T) {
	dir := realStore(t)
	claude := findClaude(t)

	before, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(%s): %v", dir, err)
	}
	out, err := exec.Command(claude, "agents", "--json", "--all").Output()
	if err != nil {
		t.Skipf("`%s agents --json --all` failed, nothing to compare against: %v", claude, err)
	}
	after, err := Load(dir)
	if err != nil {
		t.Fatalf("Load(%s) after the CLI call: %v", dir, err)
	}

	var listed []struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Skipf("`claude agents --json --all` printed something this test cannot read: %v", err)
	}

	cli := map[string]string{} // short -> session id
	cliNames := map[string]string{}
	for _, a := range listed {
		// A row with no short id is a transcript the CLI recovered with no
		// job record behind it. This package deliberately does not invent
		// those: with no short id the panel has nothing to address the
		// session by and no card could name it.
		if a.ID == "" {
			continue
		}
		cli[a.ID] = a.SessionID
		cliNames[a.ID] = a.Name
	}
	if len(cli) == 0 {
		t.Skip("the agents view listed no sessions with a short id; nothing to compare")
	}

	// A record this package could not parse carries no session id, and the
	// CLI may well have read it. Such a record is deliberately outside the
	// comparison: it is reported precisely so it does not disappear, not as
	// a claim about what the CLI thinks of it. There are normally none.
	ids := func(records []Record) map[string]string {
		m := map[string]string{}
		for _, r := range records {
			if r.SessionID == "" {
				continue
			}
			m[r.Short] = r.SessionID
		}
		return m
	}
	beforeIDs, afterIDs := ids(before), ids(after)
	steady := map[string]string{} // present both before and after the CLI ran
	everything := map[string]string{}
	for short, id := range beforeIDs {
		everything[short] = id
		if _, still := afterIDs[short]; still {
			steady[short] = id
		}
	}
	for short, id := range afterIDs {
		everything[short] = id
	}

	for short := range steady {
		if _, ok := cli[short]; !ok {
			t.Errorf("session %s is in the job store but the agents view does not list it", short)
		}
	}
	for short := range cli {
		if _, ok := everything[short]; !ok {
			t.Errorf("the agents view lists session %s, which this package did not find in the store", short)
		}
	}
	for short, id := range steady {
		if want, ok := cli[short]; ok && want != id {
			t.Errorf("session %s: store says session id %q, agents view says %q", short, id, want)
		}
	}

	// Names are compared separately from ids, and only where the store has
	// one: a renamed name field would leave every row in the panel showing a
	// bare short id, which is a real regression and an invisible one.
	named := 0
	for _, r := range before {
		want, ok := cliNames[r.Short]
		if !ok || want == "" {
			continue
		}
		if r.Name != want {
			t.Errorf("session %s: store says name %q, agents view says %q", r.Short, r.Name, want)
		}
		if r.Name != "" {
			named++
		}
	}
	if named == 0 {
		t.Error("not one real record carried a name the agents view also has — the name field has probably been renamed")
	}
	t.Logf("compared %d store records against %d agents-view rows", len(steady), len(cli))
}

// realStore returns the machine's own job store, skipping the test when
// there is none.
func realStore(t *testing.T) string {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Skipf("no home directory to find a job store in: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no job store at %s on this machine: %v", dir, err)
	}
	return dir
}

// findClaude locates the CLI the same three places internal/orchestrator
// looks: PATH first, then the two directories its installers use.
func findClaude(t *testing.T) string {
	t.Helper()
	if path, err := exec.LookPath("claude"); err == nil {
		return path
	}
	home, err := os.UserHomeDir()
	if err == nil {
		for _, p := range []string{
			filepath.Join(home, ".local", "bin", "claude"),
			filepath.Join(home, ".claude", "local", "claude"),
		} {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	t.Skip("the claude CLI is not on this machine; nothing to compare the store against")
	return ""
}
