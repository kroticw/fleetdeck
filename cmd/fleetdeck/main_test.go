package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	return dir
}

// TestRefreshDiffsAgainstThePreviousCycle pins the shape of the cycle: the snapshot
// just collected is published and the one before it is what the notification rules
// are diffed against.
func TestRefreshDiffsAgainstThePreviousCycle(t *testing.T) {
	first := state.Snapshot{At: time.Now()}
	second := state.Snapshot{
		At:       time.Now(),
		Sessions: []state.SessionView{{Session: daemon.Session{Short: "a", State: "failed"}}},
	}

	var collected int
	rec := &recorder{}
	p := newPanel(func(context.Context) state.Snapshot {
		collected++
		if collected == 1 {
			return first
		}
		return second
	}, rec, config.Default().Notify, func(error) {})

	ctx := context.Background()
	p.refresh(ctx)
	if len(rec.fired) != 0 {
		t.Fatalf("the first cycle has nothing to compare against and must fire nothing, got %v", rec.fired)
	}

	p.refresh(ctx)
	if len(rec.fired) != 1 {
		t.Fatalf("a session that has just failed must fire once, got %v", rec.fired)
	}
	if got := p.snapshot(); len(got.Sessions) != 1 {
		t.Fatal("the panel must serve the snapshot it just collected")
	}
}

// TestTheCycleRunsOneAtATime is why refresh is one function behind one mutex rather
// than the ticker's loop body copied into the watcher: two cycles running at once
// would diff against each other's snapshot and either fire a banner twice or lose one.
func TestTheCycleRunsOneAtATime(t *testing.T) {
	var inFlight, overlaps atomic.Int32
	rec := &recorder{}
	p := newPanel(func(context.Context) state.Snapshot {
		if inFlight.Add(1) > 1 {
			overlaps.Add(1)
		}
		time.Sleep(time.Millisecond)
		inFlight.Add(-1)
		return state.Snapshot{At: time.Now()}
	}, rec, config.Default().Notify, func(error) {})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.refresh(context.Background())
		}()
	}
	wg.Wait()

	if overlaps.Load() != 0 {
		t.Fatalf("the collect-and-diff cycle must never run twice at once, %d overlaps", overlaps.Load())
	}
}

// TestRefreshDoesNothingOnceTheContextIsDone covers the board watcher's callback
// still being in flight while the process shuts down: it must not start a fresh cycle
// against sources that are being torn down.
func TestRefreshDoesNothingOnceTheContextIsDone(t *testing.T) {
	var collected atomic.Int32
	p := newPanel(func(context.Context) state.Snapshot {
		collected.Add(1)
		return state.Snapshot{At: time.Now()}
	}, &recorder{}, config.Default().Notify, func(error) {})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.refresh(ctx)

	if collected.Load() != 0 {
		t.Fatal("a cycle must not start while the panel is shutting down")
	}
}

// TestPollCollectsBeforeTheFirstTick pins that the panel does not serve an empty
// snapshot for a whole poll interval after startup. An empty snapshot with a zero At
// is indistinguishable from an empty fleet to the browser.
func TestPollCollectsBeforeTheFirstTick(t *testing.T) {
	done := make(chan struct{})
	var calls atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		defer close(done)
		// An interval far longer than this test will ever wait: anything that
		// runs must have run before the first tick.
		poll(ctx, time.Hour, func(context.Context) {
			calls.Add(1)
			cancel()
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not return after its context was cancelled")
	}
	if calls.Load() != 1 {
		t.Fatalf("poll must collect once before starting the ticker, got %d calls", calls.Load())
	}
}

func TestPollKeepsTicking(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		poll(ctx, time.Millisecond, func(context.Context) {
			if calls.Add(1) >= 3 {
				cancel()
			}
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not return after its context was cancelled")
	}
	if calls.Load() < 3 {
		t.Fatalf("the ticker must keep collecting, got %d calls", calls.Load())
	}
}

// TestBoardEditsReachThePanel is the wiring board.Watch was written for and that
// nothing called: an edit made in the operator's editor must reach the panel when it
// happens, not up to a poll interval later.
func TestBoardEditsReachThePanel(t *testing.T) {
	dir := t.TempDir()
	cardsDir := filepath.Join(dir, "cards")
	if err := os.MkdirAll(cardsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		watchBoard(ctx, dir, func() {
			select {
			case changed <- struct{}{}:
			default:
			}
		})
	}()

	// Give the watcher a moment to register before touching the directory.
	time.Sleep(100 * time.Millisecond)
	writeSampleCard(t, cardsDir)

	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("a card written into the board directory must reach the panel")
	}
	cancel()
	<-done
}

// TestAnUnwatchableBoardDoesNotStopThePanel covers a board path that is missing or
// unreadable: watchBoard reports it and returns, and the panel goes on running
// without a watcher, exactly as it goes on running without a daemon.
func TestAnUnwatchableBoardDoesNotStopThePanel(t *testing.T) {
	var reported int
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchBoard(context.Background(), filepath.Join(t.TempDir(), "not-there"), func() {})
		reported++
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an unwatchable board must not leave the panel blocked forever")
	}
	if reported != 1 {
		t.Fatal("watchBoard must return rather than exiting the process")
	}
}

// TestAnUnconfiguredBoardIsNotWatched covers the panel run purely as session control,
// with no board.path at all: there is nothing to watch and nothing to report.
func TestAnUnconfiguredBoardIsNotWatched(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchBoard(context.Background(), "", func() { t.Error("nothing to watch, nothing to call") })
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an unconfigured board must not block")
	}
}

// TestCommitFailureIsAWriteThatHappened is the distinction spec section 7 calls
// mandatory: a field that reached the card and a commit that did not happen is not a
// failed write. Reported as one, the operator redoes an edit that already took
// effect, and a progress field applied twice moves somewhere nobody asked for.
func TestCommitFailureIsAWriteThatHappened(t *testing.T) {
	dir := t.TempDir() // deliberately not a git repository
	writeSampleCard(t, dir)
	card := filepath.Join(dir, "c.md")

	err := setCardField(card, "progress", "40")

	if err == nil {
		t.Fatal("a commit that did not happen must be reported, not passed off as a clean write")
	}
	if !errors.Is(err, server.ErrFieldWrittenNotCommitted) {
		t.Fatalf("want an error wrapping ErrFieldWrittenNotCommitted, got %v", err)
	}

	raw, readErr := os.ReadFile(card)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(raw), "progress: 40") {
		t.Fatal("the field must actually have reached the card")
	}
}

// TestNothingToCommitPassesThroughUnwrapped keeps board.ErrNothingToCommit reaching
// the server as itself: the card already held the value, which the server answers as
// an ordinary success and must not confuse with a commit that failed.
func TestNothingToCommitPassesThroughUnwrapped(t *testing.T) {
	dir := initRepo(t)
	writeSampleCard(t, dir)
	card := filepath.Join(dir, "c.md")
	if err := board.Commit(dir, "c.md", "test: add the card"); err != nil {
		t.Fatal(err)
	}

	// The card already carries progress: 20, so this write changes no bytes.
	err := setCardField(card, "progress", "20")

	if !errors.Is(err, board.ErrNothingToCommit) {
		t.Fatalf("want board.ErrNothingToCommit, got %v", err)
	}
	if errors.Is(err, server.ErrFieldWrittenNotCommitted) {
		t.Fatal("nothing to commit is not a commit that failed")
	}
}

func TestASuccessfulWriteIsCommitted(t *testing.T) {
	dir := initRepo(t)
	writeSampleCard(t, dir)
	card := filepath.Join(dir, "c.md")
	if err := board.Commit(dir, "c.md", "test: add the card"); err != nil {
		t.Fatal(err)
	}

	if err := setCardField(card, "stage", "review"); err != nil {
		t.Fatalf("a write into a real repository must succeed: %v", err)
	}

	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %s", out)
	}
	if len(strings.Fields(strings.TrimSpace(string(out)))) == 0 || len(strings.Split(strings.TrimSpace(string(out)), "\n")) != 2 {
		t.Fatalf("the panel's write must be recorded in the board's history, got:\n%s", out)
	}
}

// TestARefusedFieldNeverReachesTheCommit pins that a write board itself refuses is
// returned as it is: it wrote nothing, so there is nothing to say about a commit.
func TestARefusedFieldNeverReachesTheCommit(t *testing.T) {
	dir := t.TempDir()
	writeSampleCard(t, dir)

	err := setCardField(filepath.Join(dir, "c.md"), "title", "something")

	if !errors.Is(err, board.ErrUnknownField) {
		t.Fatalf("want board.ErrUnknownField, got %v", err)
	}
	if errors.Is(err, server.ErrFieldWrittenNotCommitted) {
		t.Fatal("a field that was never written must not claim it was")
	}
}

// TestBindFailureNeverLogsSuccess pins the order a startup log line is only
// allowed to happen in: bind, then log, never the reverse. An operator who
// starts a second copy of the panel by hand while a launchd-managed one
// already holds the port must see a bind failure and nothing that looks like
// a second panel starting — printing the success line before ListenAndServe
// actually succeeds means a doomed process prints "listening on" and then
// dies, leaving a log that reads as success followed by an unrelated error
// right next to a real panel it never touched.
func TestBindFailureNeverLogsSuccess(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = occupied.Close() }()
	port := occupied.Addr().(*net.TCPAddr).Port

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Default()
	cfg.ServerPort = port
	cfg.UsageEnabled = false
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	err = run(cfgPath)

	if err == nil {
		t.Fatal("binding a port already in use must be reported as an error, not a success")
	}
	if strings.Contains(logged.String(), "listening on") {
		t.Fatalf("the success line must never print before a successful bind, got log output: %s", logged.String())
	}
}

// TestSetOrchestratorSessionSurgicallyEditsAnExistingFile pins the whole
// point of routing this write through config.SetField instead of config.Save:
// a comment the operator wrote by hand must survive a pin change made from
// the picker.
func TestSetOrchestratorSessionSurgicallyEditsAnExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "orchestrator:\n" +
		"  session: old-id  # do not touch by hand\n" +
		"server:\n" +
		"  port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := NewCollector(config.Default(), nil, nil, t.TempDir())

	if err := setOrchestratorSession(p, collector, "new-id"); err != nil {
		t.Fatalf("setOrchestratorSession: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "# do not touch by hand") {
		t.Fatalf("a hand-written comment must survive a pin change, got:\n%s", got)
	}
	if !strings.Contains(got, "session: new-id") {
		t.Fatalf("the value must actually change, got:\n%s", got)
	}
	if collector.Config().OrchestratorSession != "new-id" {
		t.Fatalf("the collector must report the new pin on its next Collect, got %q", collector.Config().OrchestratorSession)
	}
}

// TestSetOrchestratorSessionCreatesAConfigWhenNoneExists covers a panel that
// has never been through `fleetdeck init`: there is nothing hand-written to
// lose, so the picker's first pin must still work rather than erroring out
// because config.SetField has no file to edit.
func TestSetOrchestratorSessionCreatesAConfigWhenNoneExists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	collector := NewCollector(config.Default(), nil, nil, t.TempDir())

	if err := setOrchestratorSession(p, collector, "first-id"); err != nil {
		t.Fatalf("setOrchestratorSession: %v", err)
	}

	got, err := config.Load(p)
	if err != nil {
		t.Fatalf("a config must have been created and must parse: %v", err)
	}
	if got.OrchestratorSession != "first-id" {
		t.Fatalf("OrchestratorSession = %q, want first-id", got.OrchestratorSession)
	}
	if collector.Config().OrchestratorSession != "first-id" {
		t.Fatal("the collector must report the new pin")
	}
}

// TestSetOrchestratorSessionNeverFallsBackToSaveForAnExistingFile guards the
// one failure config.SetField refuses to paper over: a config file that
// exists but is missing the section that would hold orchestrator.session
// (hand-edited down to something unusual). Falling back to config.Save here
// would rewrite the whole file and destroy exactly the comments this write
// path exists to protect, so the error must propagate and the file, and the
// collector's in-memory pin, must be left untouched.
func TestSetOrchestratorSessionNeverFallsBackToSaveForAnExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "server:\n  port: 7777  # hand-tuned, do not overwrite\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := NewCollector(config.Default(), nil, nil, t.TempDir())

	err := setOrchestratorSession(p, collector, "id")
	if err == nil {
		t.Fatal("a config missing the orchestrator section must be an error, not a silent full rewrite")
	}

	raw, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != content {
		t.Fatalf("a refused write must leave the file byte for byte as it was, got:\n%s", string(raw))
	}
	if collector.Config().OrchestratorSession != "" {
		t.Fatal("the collector must not report a pin that was never actually persisted")
	}
}

const testSessionUUID = "11111111-1111-1111-1111-111111111111"

// TestSetSessionLabelSurgicallyEditsAnExistingFile is setSessionLabel's own
// version of TestSetOrchestratorSessionSurgicallyEditsAnExistingFile: a
// hand-written comment on a sibling entry must survive a label write.
func TestSetSessionLabelSurgicallyEditsAnExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "session_labels:\n" +
		"    22222222-2222-2222-2222-222222222222: existing  # do not touch by hand\n" +
		"server:\n  port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := NewCollector(config.Default(), nil, nil, t.TempDir())

	if err := setSessionLabel(p, collector, testSessionUUID, "orchestrator"); err != nil {
		t.Fatalf("setSessionLabel: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "# do not touch by hand") {
		t.Fatalf("a hand-written comment on a sibling entry must survive, got:\n%s", got)
	}
	if !strings.Contains(got, testSessionUUID+": orchestrator") {
		t.Fatalf("the new entry must actually be written, got:\n%s", got)
	}
	if collector.Config().SessionLabels[testSessionUUID] != "orchestrator" {
		t.Fatalf("the collector must report the new label on its next Collect, got %+v", collector.Config().SessionLabels)
	}
}

// TestSetSessionLabelCreatesAConfigWhenNoneExists mirrors
// TestSetOrchestratorSessionCreatesAConfigWhenNoneExists: a panel that has
// never been through `fleetdeck init` must still accept the panel's first
// label rather than erroring out because config.SetSessionLabel has no file
// to edit.
func TestSetSessionLabelCreatesAConfigWhenNoneExists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	collector := NewCollector(config.Default(), nil, nil, t.TempDir())

	if err := setSessionLabel(p, collector, testSessionUUID, "first-label"); err != nil {
		t.Fatalf("setSessionLabel: %v", err)
	}

	got, err := config.Load(p)
	if err != nil {
		t.Fatalf("a config must have been created and must parse: %v", err)
	}
	if got.SessionLabels[testSessionUUID] != "first-label" {
		t.Fatalf("SessionLabels[%s] = %q, want first-label", testSessionUUID, got.SessionLabels[testSessionUUID])
	}
	if collector.Config().SessionLabels[testSessionUUID] != "first-label" {
		t.Fatal("the collector must report the new label")
	}
}

// TestSetSessionLabelCreatesTheSectionForAnExistingFileMissingIt is unlike
// its TestSetOrchestratorSessionNeverFallsBackToSaveForAnExistingFile
// sibling on purpose: session_labels is the first top-level key ever added
// to the config schema after its first release (see
// internal/config.SetSessionLabel's own comment), so a config file that
// exists but predates this feature is the ordinary case for this specific
// key, not a hand-edited oddity — and refusing it broke the feature outright
// for every operator who had already run `fleetdeck init` before today, a
// real failure caught against a real running config. config.SetSessionLabel
// now appends the missing section surgically rather than erroring, and this
// still must never fall back to a full config.Save: the hand-written
// comment on the untouched sibling key must survive.
func TestSetSessionLabelCreatesTheSectionForAnExistingFileMissingIt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "server:\n  port: 7777  # hand-tuned, do not overwrite\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	collector := NewCollector(config.Default(), nil, nil, t.TempDir())

	if err := setSessionLabel(p, collector, testSessionUUID, "label"); err != nil {
		t.Fatalf("setSessionLabel: %v", err)
	}

	raw, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatal(readErr)
	}
	got := string(raw)
	if !strings.Contains(got, "# hand-tuned, do not overwrite") {
		t.Fatalf("the existing comment must survive, got:\n%s", got)
	}
	if !strings.Contains(got, testSessionUUID+": label") {
		t.Fatalf("the new entry must actually be written, got:\n%s", got)
	}
	if collector.Config().SessionLabels[testSessionUUID] != "label" {
		t.Fatalf("the collector must report the new label, got %+v", collector.Config().SessionLabels)
	}
}

// TestSetSessionLabelEmptyRemovesTheEntryFromTheCollector pins the
// collector-side half of the "empty label deletes" contract: after a
// removal, the in-memory map must no longer report the entry at all, not an
// empty string — the same distinction internal/config.SetSessionLabel
// enforces on disk.
func TestSetSessionLabelEmptyRemovesTheEntryFromTheCollector(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(p, config.Default()); err != nil {
		t.Fatal(err)
	}
	collector := NewCollector(config.Default(), nil, nil, t.TempDir())
	if err := setSessionLabel(p, collector, testSessionUUID, "temporary"); err != nil {
		t.Fatalf("setSessionLabel: %v", err)
	}

	if err := setSessionLabel(p, collector, testSessionUUID, ""); err != nil {
		t.Fatalf("setSessionLabel(\"\"): %v", err)
	}

	if _, ok := collector.Config().SessionLabels[testSessionUUID]; ok {
		t.Fatalf("want the entry gone entirely, got %+v", collector.Config().SessionLabels)
	}
	got, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.SessionLabels[testSessionUUID]; ok {
		t.Fatalf("want the entry gone from disk too, got %+v", got.SessionLabels)
	}
}

// The terminal bridge's question "is this session still running?" is answered
// by the daemon's own list: present and not dying. A dying session is on its way
// out — the daemon marks it so about a second before its stream ends — and
// counting it as running would tell the operator a stopped session merely lost
// its connection.
func TestListedAliveCountsOnlyAPresentSessionThatIsNotDying(t *testing.T) {
	sessions := []daemon.Session{{Short: "aaaa1111"}, {Short: "bbbb2222", Dying: true}}
	for short, want := range map[string]bool{"aaaa1111": true, "bbbb2222": false, "cccc3333": false} {
		if got := listedAlive(sessions, short); got != want {
			t.Errorf("listedAlive(%q) = %v, want %v", short, got, want)
		}
	}
}
