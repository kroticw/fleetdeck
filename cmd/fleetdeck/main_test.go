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
	writeSampleCard(t, dir)

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
