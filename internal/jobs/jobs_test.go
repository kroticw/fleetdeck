package jobs

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// writeStore builds a job store in a temp directory. Each entry is a short id
// mapped to the raw bytes of its state.json; a nil value means the directory
// exists with no state.json in it at all, which is what a session that never
// got off the ground leaves behind.
func writeStore(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for short, body := range entries {
		sub := filepath.Join(dir, short)
		if err := os.MkdirAll(filepath.Join(sub, "tmp"), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
		if body == nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(sub, "state.json"), body, 0o600); err != nil {
			t.Fatalf("write state.json for %s: %v", short, err)
		}
	}
	return dir
}

func find(t *testing.T, records []Record, short string) Record {
	t.Helper()
	for _, r := range records {
		if r.Short == short {
			return r
		}
	}
	t.Fatalf("no record for %q in %v", short, records)
	return Record{}
}

func TestLoadReadsEveryFieldThePanelShows(t *testing.T) {
	cwd := t.TempDir()
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{
			"sessionId": "aaaa1111-0000-4000-8000-000000000001",
			"daemonShort": "aaaa1111",
			"name": "a stopped session",
			"cwd": "` + cwd + `",
			"state": "done",
			"detail": "finished the thing",
			"intent": "do the thing",
			"backend": "daemon",
			"cliVersion": "2.1.263",
			"createdAt": "2026-09-11T18:08:55.055Z",
			"updatedAt": "2026-09-12T04:01:16.231Z"
		}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d: %v", len(records), records)
	}
	r := records[0]
	if r.Short != "aaaa1111" {
		t.Errorf("Short = %q", r.Short)
	}
	if r.SessionID != "aaaa1111-0000-4000-8000-000000000001" {
		t.Errorf("SessionID = %q", r.SessionID)
	}
	if r.Name != "a stopped session" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.CWD != cwd {
		t.Errorf("CWD = %q", r.CWD)
	}
	if r.State != "done" || r.Detail != "finished the thing" || r.Intent != "do the thing" {
		t.Errorf("State/Detail/Intent = %q/%q/%q", r.State, r.Detail, r.Intent)
	}
	if r.Backend != "daemon" || r.CLIVersion != "2.1.263" {
		t.Errorf("Backend/CLIVersion = %q/%q", r.Backend, r.CLIVersion)
	}
	wantCreated := time.Date(2026, 9, 11, 18, 8, 55, 55_000_000, time.UTC)
	if !r.CreatedAt.Equal(wantCreated) {
		t.Errorf("CreatedAt = %v, want %v", r.CreatedAt, wantCreated)
	}
	wantUpdated := time.Date(2026, 9, 12, 4, 1, 16, 231_000_000, time.UTC)
	if !r.UpdatedAt.Equal(wantUpdated) {
		t.Errorf("UpdatedAt = %v, want %v", r.UpdatedAt, wantUpdated)
	}
	if !r.Resumable {
		t.Error("Resumable = false, want true: the session id is there and the cwd exists")
	}
}

// The short id is the directory the record lives in, never the daemonShort
// field inside it: the directory name is the key everything else looks a
// session up by, so a file disagreeing with its own directory must not be
// able to make this package answer about a different session.
func TestShortComesFromTheDirectoryNotTheFile(t *testing.T) {
	cwd := t.TempDir()
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "x", "daemonShort": "bbbb2222", "cwd": "` + cwd + `"}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 1 || records[0].Short != "aaaa1111" {
		t.Fatalf("want one record shorted aaaa1111, got %v", records)
	}
}

// pins.json is a real file the agents view keeps beside the session
// directories (see the package doc). Reading it as a session would produce a
// record named "pins.json".
func TestLoadIgnoresFilesBesideTheSessionDirectories(t *testing.T) {
	dir := writeStore(t, map[string][]byte{"aaaa1111": []byte(`{"sessionId": "x"}`)})
	if err := os.WriteFile(filepath.Join(dir, "pins.json"), []byte(`["aaaa1111"]`), 0o600); err != nil {
		t.Fatalf("write pins.json: %v", err)
	}

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 1 || records[0].Short != "aaaa1111" {
		t.Fatalf("want only the session directory, got %v", records)
	}
}

// A directory with no state.json is not a session. `claude agents --all`
// does not list one either, and this package's whole claim is that it sees
// the same sessions that view does.
func TestLoadSkipsDirectoriesWithNoState(t *testing.T) {
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "x"}`),
		"bbbb2222": nil,
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 1 || records[0].Short != "aaaa1111" {
		t.Fatalf("want only the session with state, got %v", records)
	}
}

// A record whose state.json cannot be parsed is reported, not dropped.
// Dropping it is the failure this whole package exists to end: the session
// vanishes from the panel and its board card is marked orphaned, which says
// "this card lost its session" about a session that is sitting right there.
// Reported with nothing but its short id it is honestly unresumable — there
// is no session id to resume — which is exactly what it is.
func TestLoadReportsAnUnreadableRecordRatherThanHidingIt(t *testing.T) {
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "x"`), // truncated on purpose
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want the broken record reported, got %v", records)
	}
	r := records[0]
	if r.Short != "aaaa1111" {
		t.Errorf("Short = %q", r.Short)
	}
	if r.SessionID != "" {
		t.Errorf("SessionID = %q, want empty: nothing was parsed", r.SessionID)
	}
	if r.Resumable {
		t.Error("Resumable = true on a record that could not be read")
	}
}

// Resumability is the saved working directory still existing, and nothing
// else — matched against claude-agents-mcp's own rule (internal/agents/
// resume.go's Resumable/cwdMissing), which is what the agents view answers
// with. A deleted worktree is the common case on this machine.
func TestResumableFollowsTheWorkingDirectory(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted-worktree")
	alive := t.TempDir()
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "a", "cwd": "` + alive + `"}`),
		"bbbb2222": []byte(`{"sessionId": "b", "cwd": "` + gone + `"}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !find(t, records, "aaaa1111").Resumable {
		t.Error("a session whose cwd exists must be resumable")
	}
	if find(t, records, "bbbb2222").Resumable {
		t.Error("a session whose cwd is gone must not be resumable")
	}
}

// A file where a directory is expected is not a directory: resuming into it
// crashes the same way a missing one does.
func TestResumableRejectsACwdThatIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "a", "cwd": "` + file + `"}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if records[0].Resumable {
		t.Error("a cwd that is a file must not read as resumable")
	}
}

// An empty cwd is not a missing one: the daemon falls back to a default, so
// the session still resumes. claude-agents-mcp's cwdMissing makes the same
// distinction explicitly, and disagreeing with it would put a resumable
// session in the "cannot be brought back" group.
func TestEmptyWorkingDirectoryIsStillResumable(t *testing.T) {
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "a"}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !records[0].Resumable {
		t.Error("an absent cwd must not make a session unresumable")
	}
}

// resumeSessionId is what the daemon actually resumes by; sessionId is the
// transcript's own id. They are the same on an ordinary session and differ
// on one that was resumed from another, and taking the wrong one would
// resume the wrong conversation.
func TestResumeSessionIDWinsOverSessionID(t *testing.T) {
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "first", "resumeSessionId": "second"}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := records[0].ResumeSessionID; got != "second" {
		t.Errorf("ResumeSessionID = %q, want second", got)
	}
	if got := records[0].SessionID; got != "first" {
		t.Errorf("SessionID = %q, want first: the transcript id is not replaced", got)
	}
}

// With no id of either kind there is nothing to resume, however alive the
// working directory is.
func TestNoSessionIDIsNotResumable(t *testing.T) {
	alive := t.TempDir()
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"cwd": "` + alive + `"}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if records[0].Resumable {
		t.Error("a record with no session id must not read as resumable")
	}
}

// The order is the store's own, sorted, so two collect cycles a second apart
// cannot reorder the panel's list under a person's cursor for no reason.
func TestLoadIsSortedByShort(t *testing.T) {
	dir := writeStore(t, map[string][]byte{
		"cccc3333": []byte(`{"sessionId": "c"}`),
		"aaaa1111": []byte(`{"sessionId": "a"}`),
		"bbbb2222": []byte(`{"sessionId": "b"}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := []string{records[0].Short, records[1].Short, records[2].Short}
	want := []string{"aaaa1111", "bbbb2222", "cccc3333"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// A machine that has never run a background session has no job store at all.
// That is an empty fleet, not a failure, and it must not put an error banner
// on a panel that is working perfectly.
func TestLoadOnAMissingStoreIsEmptyNotAnError(t *testing.T) {
	records, err := Load(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatalf("Load on a missing store: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("want no records, got %v", records)
	}
}

// A store that exists but cannot be read is a real failure and must be
// reported: the panel has to say the stopped sessions are unknown rather
// than quietly show none, which looks exactly like a fleet where nothing is
// stopped.
func TestLoadReportsAnUnreadableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory regardless of its mode")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := Load(dir); err == nil {
		t.Fatal("want an error from an unreadable store, got nil")
	}
}

// A timestamp the store writes in a shape this package does not expect is
// not worth losing the whole record over: the times are for ordering rows,
// the identity and the resumability are what the panel is actually for.
func TestUnparseableTimestampsLeaveTheRecordUsable(t *testing.T) {
	dir := writeStore(t, map[string][]byte{
		"aaaa1111": []byte(`{"sessionId": "a", "createdAt": "yesterday", "updatedAt": ""}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r := records[0]
	if r.SessionID != "a" || !r.Resumable {
		t.Errorf("record lost to a bad timestamp: %+v", r)
	}
	if !r.CreatedAt.IsZero() || !r.UpdatedAt.IsZero() {
		t.Errorf("want zero times, got %v / %v", r.CreatedAt, r.UpdatedAt)
	}
}

// The two fields below are read for one caller only — the resume descriptor
// internal/daemon.Client.Resume sends — and for nothing the panel displays.
// They are tested apart from the display fields above for that reason: a
// change that drops one of them resumes the session without its own flags,
// which is invisible in every screen this package otherwise feeds and shows
// up only as a session that came back on the wrong model.
func TestLoadReadsTheFieldsAResumeDescriptorNeeds(t *testing.T) {
	cwd := t.TempDir()
	dir := writeStore(t, map[string][]byte{
		"bbbb2222": []byte(`{
			"sessionId": "bbbb2222-0000-4000-8000-000000000002",
			"resumeSessionId": "cccc3333-0000-4000-8000-000000000003",
			"cwd": "` + cwd + `",
			"linkScanPath": "/somewhere/projects/-p/bbbb2222.jsonl",
			"respawnFlags": ["--name", "a session", "--model", "opus"]
		}`),
	})

	records, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r := find(t, records, "bbbb2222")

	if got, want := r.LinkScanPath, "/somewhere/projects/-p/bbbb2222.jsonl"; got != want {
		t.Errorf("LinkScanPath = %q, want %q", got, want)
	}
	want := []string{"--name", "a session", "--model", "opus"}
	if !slices.Equal(r.RespawnFlags, want) {
		t.Errorf("RespawnFlags = %v, want %v", r.RespawnFlags, want)
	}
}

// ResumeID is the rule "resumeSessionId, else sessionId" made callable. It is
// exported because the resume path outside this package has to apply exactly
// the same rule Resumable applied inside it — two copies of that rule could
// disagree, and a session reported resumable would then be dispatched under
// an id the daemon does not know.
func TestResumeIDPrefersTheExplicitResumeID(t *testing.T) {
	both := Record{SessionID: "s", ResumeSessionID: "r"}
	if got := both.ResumeID(); got != "r" {
		t.Errorf("ResumeID with both set = %q, want %q", got, "r")
	}
	only := Record{SessionID: "s"}
	if got := only.ResumeID(); got != "s" {
		t.Errorf("ResumeID with no resume id = %q, want %q", got, "s")
	}
	if got := (Record{}).ResumeID(); got != "" {
		t.Errorf("ResumeID with neither = %q, want empty", got)
	}
}
