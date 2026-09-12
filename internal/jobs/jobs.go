// Package jobs reads Claude Code's own job store — the directory tree under
// ~/.claude/jobs where the daemon keeps one record per background session it
// has ever started.
//
// It exists because the daemon's control socket cannot answer the question
// "which sessions are stopped". A session's presence in a `list` reply is
// what marks it alive (docs/protocol/daemon-control-socket.md section 4); a
// session that has been stopped simply stops being an element of the reply,
// carrying no flag and leaving no trace. Asked over the socket alone, a
// stopped session and a session that never existed are the same answer — and
// the panel drew that answer literally, dropping the row and marking its
// board card orphaned, for a session that was sitting on disk ready to be
// resumed with its whole history.
//
// # This is a foreign format, and that is the cost of the feature
//
// Nothing here is a contract. The layout and the field names belong to Claude
// Code, which writes them for its own agents view and is free to change them
// in any release; this package only reads them. That dependency is real and is
// named on purpose rather than hidden — docs/protocol/claude-jobs-store.md
// carries the whole of it, including what breaks if the format moves and how
// it is noticed. The read is deliberately shallow: identity, a name, a working
// directory and the last state, all optional, none of it interpreted beyond
// what the panel prints. A record this package cannot parse is still reported,
// carrying its short id alone, because a session vanishing from the panel is
// the exact failure the package was written to stop.
//
// The layout, as of Claude Code 2.1.263:
//
//	~/.claude/jobs/
//	  pins.json          — the agents view's pin set; not a session
//	  <short>/           — one directory per session, named by its short id
//	    state.json       — the record this package reads
//	    timeline.jsonl   — the session's own event log; not read here
//	    tmp/             — the session's scratch directory; not read here
//
// # Why this package walks the store instead of asking the CLI
//
// The agents view's own list comes from `claude agents --json --all`, which
// walks this same directory from inside the CLI. fleetdeck could shell out to
// it and inherit its answer exactly — but the panel polls every two seconds
// (internal/config's DaemonPollInterval), which is forty thousand child
// processes a day to learn about thirty rows, and it would make the panel
// stop working on a machine where the CLI is not on PATH. Walking the store
// costs a readdir and a few dozen 2 KB reads, and it answers the same: checked
// against `claude agents --json --all` on 2026-09-12, the two lists agreed on
// every short id and every resumability verdict. See
// store_real_test.go, which makes that comparison a test rather than a claim.
package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// stateFile is the name of the record inside a session's directory.
const stateFile = "state.json"

// Record is one session as the job store knows it.
//
// Every field is optional, because every field in the file is: this package
// reports what it found and never invents a value for what it did not. Short
// is the only field that is always set, and it comes from the directory name
// rather than from any field inside the file (see Load).
type Record struct {
	// Short is the session's short id — the name of its directory in the
	// store, which is the id everything else addresses a session by.
	Short string

	// SessionID is the transcript UUID, and ResumeSessionID is what the
	// daemon actually resumes by. They are equal on an ordinary session and
	// differ on one resumed from another, which is why both are carried:
	// the transcript is found by the first, the session is brought back by
	// the second.
	SessionID       string
	ResumeSessionID string

	// Name is the session's display name, CWD its saved working directory.
	Name string
	CWD  string

	// State, Detail and Intent are the last values the session recorded
	// before it stopped. They are the daemon's own vocabulary, frozen at the
	// moment it went away — never a reading of anything happening now.
	State  string
	Detail string
	Intent string

	// Backend and CLIVersion describe how the session ran.
	Backend    string
	CLIVersion string

	// CreatedAt and UpdatedAt are when the session was started and when it
	// last wrote anything. Zero means the store carried no timestamp this
	// package could read; nothing derives a duration from a zero.
	CreatedAt time.Time
	UpdatedAt time.Time

	// Resumable reports whether this session can be brought back in place,
	// with its full history, as opposed to being really dead. The rule is
	// claude-agents-mcp's own (internal/agents/resume.go): it needs an id to
	// resume by, and its saved working directory has to still exist — a
	// deleted worktree being far and away the most common way a session
	// stops being resumable. An empty CWD is not a missing one: the daemon
	// falls back to a default, so such a session still resumes.
	Resumable bool
}

// state is the subset of state.json this package reads. Every field is
// optional and none is required to produce a usable record.
type state struct {
	SessionID       string `json:"sessionId"`
	ResumeSessionID string `json:"resumeSessionId"`
	Name            string `json:"name"`
	CWD             string `json:"cwd"`
	State           string `json:"state"`
	Detail          string `json:"detail"`
	Intent          string `json:"intent"`
	Backend         string `json:"backend"`
	CLIVersion      string `json:"cliVersion"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// Dir returns the job store's location, ~/.claude/jobs.
//
// Hardcoded under the home directory, the same way internal/daemon finds the
// control key and cmd/fleetdeck finds the transcripts: Claude Code itself
// hardcodes it too (claude-agents-mcp's jobsDir does the same), so a
// configurable path here would only be able to point at somewhere the daemon
// never writes.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "jobs"), nil
}

// Load reads every session record in the store at dir, sorted by short id.
//
// A store that does not exist is not a failure: a machine that has never run
// a background session has no job directory, and that is an empty fleet
// rather than something to put an error banner over. A store that exists and
// cannot be read is a real failure and is returned as one — reporting no
// stopped sessions would be indistinguishable from a fleet where none are.
//
// Anything in the store that is not a session directory — pins.json, any
// stray file — is skipped, as is a directory holding no state.json at all
// (a session that never got far enough to have one; `claude agents --all`
// does not list those either).
//
// A state.json that cannot be read or parsed still produces a record, with
// its short id and nothing else. It reads as not resumable, which is the
// truth about a session whose id cannot be recovered, and it keeps the
// session visible instead of making it disappear — the failure this package
// exists to end.
func Load(dir string) ([]Record, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read job store %s: %w", dir, err)
	}

	records := make([]Record, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		short := e.Name()
		path := filepath.Join(dir, short, stateFile)
		body, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			// Readable directory, unreadable record: the session exists and
			// there is nothing to say about it but its short id.
			records = append(records, Record{Short: short})
			continue
		}
		records = append(records, parse(short, body))
	}

	sort.Slice(records, func(i, j int) bool { return records[i].Short < records[j].Short })
	return records, nil
}

// parse turns one state.json into a record. short comes from the directory,
// never from the file: the directory name is the id every other part of the
// system addresses this session by, so a file disagreeing with the directory
// it sits in must not be able to make this package answer about a different
// session.
func parse(short string, body []byte) Record {
	var s state
	if err := json.Unmarshal(body, &s); err != nil {
		return Record{Short: short}
	}
	r := Record{
		Short:           short,
		SessionID:       s.SessionID,
		ResumeSessionID: s.ResumeSessionID,
		Name:            s.Name,
		CWD:             s.CWD,
		State:           s.State,
		Detail:          s.Detail,
		Intent:          s.Intent,
		Backend:         s.Backend,
		CLIVersion:      s.CLIVersion,
		CreatedAt:       parseTime(s.CreatedAt),
		UpdatedAt:       parseTime(s.UpdatedAt),
	}
	r.Resumable = r.resumeID() != "" && !cwdMissing(r.CWD)
	return r
}

// resumeID is the id the daemon would resume this session by: the explicit
// resume id when the store carries one, the transcript id otherwise.
func (r Record) resumeID() string {
	if r.ResumeSessionID != "" {
		return r.ResumeSessionID
	}
	return r.SessionID
}

// cwdMissing reports whether a session's saved working directory is gone. An
// empty path is treated as present, not missing: the daemon falls back to a
// default, so a record with no cwd still resumes. This mirrors
// claude-agents-mcp's function of the same name deliberately, including that
// distinction — the two must agree, or the panel and the agents view would
// disagree about which sessions can be brought back.
func cwdMissing(cwd string) bool {
	if strings.TrimSpace(cwd) == "" {
		return false
	}
	fi, err := os.Stat(cwd)
	return err != nil || !fi.IsDir()
}

// parseTime reads one of the store's timestamps. An unparseable or absent
// value yields the zero time rather than losing the record it belongs to:
// the times only order rows, while the identity and the resumability are
// what the panel is actually for.
func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
