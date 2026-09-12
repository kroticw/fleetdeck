package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
)

// The panel used to drop a stopped session on the floor. The daemon's list
// reply is the live sessions and nothing else, and the panel had no other
// source, so a session someone stopped with `claude stop` left no row, no
// counter and no trace but a board card the panel then called orphaned —
// while the session sat in the job store with its whole history, one resume
// away. These tests are about that session being in the list, and about it
// being in the list as what it is rather than as something pretending to run.

// writeJobStore lays out a job store the way Claude Code does and points the
// collector at it. Each entry is a short id mapped to the body of its
// state.json; "@cwd" anywhere in the body is replaced with a directory that
// exists, which is what makes a record resumable.
func writeJobStore(t *testing.T, entries map[string]string) string {
	t.Helper()
	store := t.TempDir()
	cwd := t.TempDir()
	for short, body := range entries {
		dir := filepath.Join(store, short)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body = strings.ReplaceAll(body, "@cwd", cwd)
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	previous := jobStoreDir
	jobStoreDir = store
	t.Cleanup(func() { jobStoreDir = previous })
	return store
}

func sessionByShort(t *testing.T, snap state.Snapshot, short string) state.SessionView {
	t.Helper()
	for _, s := range snap.Sessions {
		if s.Short == short {
			return s
		}
	}
	t.Fatalf("no session %q in the snapshot; it holds %v", short, shorts(snap))
	return state.SessionView{}
}

func shorts(snap state.Snapshot) []string {
	out := make([]string, 0, len(snap.Sessions))
	for _, s := range snap.Sessions {
		out = append(out, s.Short)
	}
	return out
}

// boardWith writes one card naming a session and returns the board path.
func boardWith(t *testing.T, session string) string {
	t.Helper()
	dir := t.TempDir()
	cards := filepath.Join(dir, "cards")
	if err := os.MkdirAll(cards, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\nzone: planned\nstage: active\nprogress: 20\nsession: " + session +
		"\nrepo: x/y\ncreated: 2026-09-09\n---\n\n# card\n"
	if err := os.WriteFile(filepath.Join(cards, "c.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func collectWith(t *testing.T, boardPath string, daemonJobs string) state.Snapshot {
	t.Helper()
	cfg := config.Default()
	cfg.BoardPath = boardPath
	cfg.UsageEnabled = false
	dc := deadDaemon(t)
	if daemonJobs != "" {
		dc = fakeDaemon(t, daemonJobs)
	}
	return NewCollector(cfg, dc, nil, t.TempDir()).Collect(context.Background())
}

func TestStoppedSessionStaysInTheListAsStopped(t *testing.T) {
	writeJobStore(t, map[string]string{
		"stop1234": `{"sessionId":"` + sampleUUID + `","name":"a stopped agent",
			"cwd":"@cwd","state":"done","detail":"finished the thing",
			"tempo":"blocked","updatedAt":"2026-09-12T04:00:00Z"}`,
	})

	snap := collectWith(t, t.TempDir(), `{"short":"live1234","state":"working"}`)

	if len(snap.Sessions) != 2 {
		t.Fatalf("want the live session and the stopped one, got %v", shorts(snap))
	}
	s := sessionByShort(t, snap, "stop1234")
	if s.Lifecycle != state.LifecycleStopped {
		t.Errorf("lifecycle = %q, want %q", s.Lifecycle, state.LifecycleStopped)
	}
	if !s.Resumable() {
		t.Error("a session whose working directory exists must read as resumable")
	}
	if s.Name != "a stopped agent" {
		t.Errorf("name = %q, want the store's own", s.Name)
	}
	if s.LastState != "done" || s.Detail != "finished the thing" {
		t.Errorf("the last state the session recorded must be carried: %q / %q", s.LastState, s.Detail)
	}
	if s.State != "" {
		t.Errorf("state = %q: a session that is not running is in no state right now", s.State)
	}
	if sessionByShort(t, snap, "live1234").Lifecycle != state.LifecycleLive {
		t.Error("the daemon's own session must be stamped live")
	}
}

// Tempo, needs and dying are readings of a running process. Carried over
// from a frozen record they would make a session that stopped this morning
// go on claiming a person's attention: a stopped session with tempo
// "blocked" reads as stalled everywhere that rule is applied, and the
// counters in the header would count it every poll, forever.
func TestStoppedSessionCarriesNoLiveReadings(t *testing.T) {
	writeJobStore(t, map[string]string{
		"stop1234": `{"sessionId":"` + sampleUUID + `","cwd":"@cwd",
			"tempo":"blocked","needs":"answer: still there?","state":"blocked"}`,
	})

	snap := collectWith(t, t.TempDir(), "")
	s := sessionByShort(t, snap, "stop1234")

	if s.Tempo != "" {
		t.Errorf("tempo = %q, want empty on a session that is not running", s.Tempo)
	}
	// Stated, not omitted: a stopped session is known not to be waiting, so Needs
	// carries an empty string rather than nothing at all. Nil here would make
	// Waiting() answer "unknown" and draw the session as an open question forever.
	if s.Needs == nil {
		t.Error("needs is absent; a stopped session must say it has no question, not stay silent")
	} else if *s.Needs != "" {
		t.Errorf("needs = %q, want empty: nobody is being asked anything", *s.Needs)
	}
	if got := s.Waiting(); got != daemon.No {
		t.Errorf("Waiting() = %v on a stopped session, want no", got)
	}
	if s.Dying {
		t.Error("a stopped session is not dying; it has already stopped")
	}
	if s.PID != 0 || s.StartedAt != 0 {
		t.Errorf("pid/startedAt = %d/%d, want zero: nothing is running", s.PID, s.StartedAt)
	}
	if s.State != "" {
		t.Errorf("state = %q, want empty: the frozen value goes to LastState, which no rule reads", s.State)
	}
	if s.LastState != "blocked" {
		t.Errorf("lastState = %q, want the value the session recorded", s.LastState)
	}
	if s.Waiting() == daemon.Yes || s.Stalled() {
		t.Error("a stopped session must land in neither the waiting nor the stalled counter")
	}
}

// Silence is the age of the last write to a transcript nothing is writing
// any more, and the context bar is a reading frozen at the moment the
// session stopped. Measured for a stopped session, the first says it has
// been silent since it stopped — which is true and useless — and the second
// draws a stale number as if it were current.
func TestStoppedSessionIsNotMeasuredForSilenceOrContext(t *testing.T) {
	projects := t.TempDir()
	writeTranscript(t, projects)
	writeJobStore(t, map[string]string{
		"stop1234": `{"sessionId":"` + sampleUUID + `","cwd":"@cwd"}`,
	})

	cfg := config.Default()
	cfg.BoardPath = t.TempDir()
	cfg.UsageEnabled = false
	snap := NewCollector(cfg, deadDaemon(t), nil, projects).Collect(context.Background())

	s := sessionByShort(t, snap, "stop1234")
	if s.SilentFor != 0 {
		t.Errorf("silentFor = %v, want unmeasured", s.SilentFor)
	}
	if s.Context != nil {
		t.Errorf("context = %+v, want none", s.Context)
	}
}

// A session whose working directory is gone cannot be resumed — the common
// case being a deleted worktree. It is still shown, because the work was
// real and a person has to be able to see that it is not coming back, but it
// must not be offered as something that can be picked up again.
func TestSessionWithNoWorkingDirectoryIsDeadNotStopped(t *testing.T) {
	writeJobStore(t, map[string]string{
		"dead1234": `{"sessionId":"` + sampleUUID + `","cwd":"/nowhere/deleted-worktree"}`,
	})

	snap := collectWith(t, t.TempDir(), "")
	s := sessionByShort(t, snap, "dead1234")

	if s.Lifecycle != state.LifecycleDead {
		t.Errorf("lifecycle = %q, want %q", s.Lifecycle, state.LifecycleDead)
	}
	if s.Resumable() {
		t.Error("a session with no working directory must not read as resumable")
	}
	if s.Live() {
		t.Error("a dead session must not read as live")
	}
}

// Every live session has a record in the store too — the store is where the
// daemon writes it. The live reply is the authority on a running session and
// the record is a frozen copy of what it last wrote, so the record must be
// dropped rather than shown beside it.
func TestALiveSessionIsNotDuplicatedByItsOwnRecord(t *testing.T) {
	writeJobStore(t, map[string]string{
		"live1234": `{"sessionId":"` + sampleUUID + `","cwd":"@cwd","state":"done","detail":"stale"}`,
	})

	snap := collectWith(t, t.TempDir(), `{"short":"live1234","state":"working","detail":"current"}`)

	if len(snap.Sessions) != 1 {
		t.Fatalf("want one row for one session, got %v", shorts(snap))
	}
	s := snap.Sessions[0]
	if s.Lifecycle != state.LifecycleLive {
		t.Errorf("lifecycle = %q, want live", s.Lifecycle)
	}
	if s.Detail != "current" {
		t.Errorf("detail = %q: the daemon's live value must win over the record on disk", s.Detail)
	}
}

// The board half of the same lie. A card whose agent was stopped was marked
// "card lost its session" while the session was sitting in the store.
func TestStoppedSessionsCardIsNotOrphaned(t *testing.T) {
	writeJobStore(t, map[string]string{
		"stop1234": `{"sessionId":"` + sampleUUID + `","cwd":"@cwd"}`,
	})

	snap := collectWith(t, boardWith(t, "stop1234"), "")

	if len(snap.OrphanCards) != 0 {
		t.Errorf("a card whose session is merely stopped must not be orphaned: %v", snap.OrphanCards)
	}
	if len(snap.StoppedCards) != 1 {
		t.Errorf("the card must be reported as stopped instead: %v", snap.StoppedCards)
	}
}

func TestDeadSessionsCardStaysOrphaned(t *testing.T) {
	writeJobStore(t, map[string]string{
		"dead1234": `{"sessionId":"` + sampleUUID + `","cwd":"/nowhere/deleted-worktree"}`,
	})

	snap := collectWith(t, boardWith(t, "dead1234"), "")

	if len(snap.OrphanCards) != 1 {
		t.Errorf("a card whose session cannot come back is an orphan: %v", snap.OrphanCards)
	}
	if len(snap.StoppedCards) != 0 {
		t.Errorf("a dead session's card is not merely stopped: %v", snap.StoppedCards)
	}
}

// A card naming a session that is in no list at all — not the daemon's, not
// the store's — is the case orphaning was written for, and it still is.
func TestCardNamingNothingAtAllStaysOrphaned(t *testing.T) {
	writeJobStore(t, map[string]string{})

	snap := collectWith(t, boardWith(t, "gone1234"), "")

	if len(snap.OrphanCards) != 1 {
		t.Errorf("a card naming nothing must stay orphaned: %v", snap.OrphanCards)
	}
}

// A store that cannot be read is reported, and the live sessions still
// arrive: a source that failed fills its own field and leaves the rest of
// the snapshot alone. Showing no stopped sessions silently would look
// exactly like a fleet where nothing is stopped.
func TestUnreadableJobStoreIsReportedAndKeepsTheLiveSessions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 directory regardless of its mode")
	}
	store := writeJobStore(t, map[string]string{})
	if err := os.Chmod(store, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o700) })

	snap := collectWith(t, t.TempDir(), `{"short":"live1234","state":"working"}`)

	if snap.JobsError == "" {
		t.Error("an unreadable job store must be reported, not shown as an empty one")
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Short != "live1234" {
		t.Errorf("the live sessions must survive a failed store read: %v", shorts(snap))
	}
	if snap.DaemonError != "" {
		t.Errorf("the daemon did not fail: %q", snap.DaemonError)
	}
}

// The stopped ones come after every live one, and among themselves the most
// recently active first — the session just stopped is the one a person is
// looking for, not whichever short id sorts first.
func TestStoppedSessionsFollowTheLiveOnesNewestFirst(t *testing.T) {
	writeJobStore(t, map[string]string{
		"aaaaold1": `{"sessionId":"` + sampleUUID + `","cwd":"@cwd","updatedAt":"2026-09-01T10:00:00Z"}`,
		"zzzznew1": `{"sessionId":"` + sampleUUID + `","cwd":"@cwd","updatedAt":"2026-09-12T10:00:00Z"}`,
		"mmmnone1": `{"sessionId":"` + sampleUUID + `","cwd":"@cwd"}`,
	})

	snap := collectWith(t, t.TempDir(), `{"short":"live1234","state":"working"}`)

	want := []string{"live1234", "zzzznew1", "aaaaold1", "mmmnone1"}
	got := shorts(snap)
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// The operator's own name for a session is the one thing that does survive
// it stopping: it names the work, not the process.
func TestStoppedSessionKeepsItsLabel(t *testing.T) {
	writeJobStore(t, map[string]string{
		"stop1234": `{"sessionId":"` + sampleUUID + `","name":"daemon name","cwd":"@cwd"}`,
	})

	cfg := config.Default()
	cfg.BoardPath = t.TempDir()
	cfg.UsageEnabled = false
	cfg.SessionLabels = map[string]string{sampleUUID: "what the operator calls it"}
	snap := NewCollector(cfg, deadDaemon(t), nil, t.TempDir()).Collect(context.Background())

	if got := sessionByShort(t, snap, "stop1234").Label; got != "what the operator calls it" {
		t.Errorf("label = %q, want the operator's own", got)
	}
}
