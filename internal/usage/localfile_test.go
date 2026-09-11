package usage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleLocalLimits() Limits {
	return Limits{
		FiveHour:  Window{Utilization: 13, ResetsAt: time.Date(2026, 9, 11, 6, 0, 0, 0, time.UTC)},
		SevenDay:  Window{Utilization: 40, ResetsAt: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)},
		FetchedAt: time.Date(2026, 9, 11, 0, 10, 0, 0, time.UTC),
	}
}

func TestWriteLocalThenReadLocalRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	want := sampleLocalLimits()

	if err := WriteLocal(path, want); err != nil {
		t.Fatalf("WriteLocal: %v", err)
	}
	got, err := ReadLocal(path)
	if err != nil {
		t.Fatalf("ReadLocal: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestReadLocalWithNoFileIsErrNoLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	if _, err := ReadLocal(path); !errors.Is(err, ErrNoLocalFile) {
		t.Fatalf("want ErrNoLocalFile, got %v", err)
	}
}

func TestWriteLocalCreatesTheDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "rate_limits.json")
	if err := WriteLocal(path, sampleLocalLimits()); err != nil {
		t.Fatalf("WriteLocal into a non-existent directory: %v", err)
	}
	if _, err := ReadLocal(path); err != nil {
		t.Fatalf("ReadLocal after WriteLocal created the directory: %v", err)
	}
}

// TestWriteLocalNeverUsesTMPDIR is the fix the atomic-write requirement
// exists for: os.Rename is only atomic within a single filesystem, and
// $TMPDIR can be on a different one from the target directory on this
// machine. Pointing TMPDIR at a directory that does not exist at all is
// the cheapest way to prove WriteLocal's temp file never goes there --
// if it did, this write would fail outright, not just lose atomicity.
func TestWriteLocalNeverUsesTMPDIR(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	if err := WriteLocal(path, sampleLocalLimits()); err != nil {
		t.Fatalf("WriteLocal failed with TMPDIR pointed at a missing directory -- it must never use TMPDIR at all: %v", err)
	}
}

// TestReadLocalRejectsAFileWithNoFetchedAt is the fix this package exists
// for: a value with no recorded write time is indistinguishable from a
// value that just happened, and the panel's whole "show the age, do not
// pretend it is live" requirement depends on that time actually being
// there.
func TestReadLocalRejectsAFileWithNoFetchedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	if err := os.WriteFile(path, []byte(`{"fiveHour":{"utilization":13},"sevenDay":{"utilization":40}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocal(path); err == nil {
		t.Fatal("a file with no fetchedAt must be rejected, not read as if it just happened")
	}
}

func TestReadLocalRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLocal(path); err == nil {
		t.Fatal("garbage content must be reported as an error, not read as a zero Limits")
	}
}

// TestConcurrentWriteLocalNeverLeavesGarbage is the fix the atomic-write
// requirement exists for: with several sessions writing at once (the real
// scenario this task's own spec names), a plain write racing another one's
// would leave a reader seeing a half-written file -- indistinguishable
// from corruption, and worse than either write simply losing the race.
// This does not prove no race is possible (no test can, at this level);
// it proves every read after concurrent writers finish gets one of the
// values written, in full, never a mix of two.
func TestConcurrentWriteLocalNeverLeavesGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits.json")
	const writers = 20
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := sampleLocalLimits()
			l.FiveHour.Utilization = float64(i)
			if err := WriteLocal(path, l); err != nil {
				t.Errorf("WriteLocal from writer %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := ReadLocal(path)
	if err != nil {
		t.Fatalf("ReadLocal after concurrent writers: %v", err)
	}
	if got.FiveHour.Utilization < 0 || got.FiveHour.Utilization >= writers {
		t.Fatalf("read back a value no writer wrote: %+v", got)
	}
}

func TestAppendTraceCreatesTheFileAndDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "rate_limits_trace.log")
	if err := AppendTrace(path, "five_hour=true seven_day=false"); err != nil {
		t.Fatalf("AppendTrace: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file: %v", err)
	}
	if !strings.Contains(string(body), "five_hour=true seven_day=false") {
		t.Fatalf("trace file does not contain the line written: %q", body)
	}
}

func TestAppendTraceAppendsRatherThanOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	if err := AppendTrace(path, "first line"); err != nil {
		t.Fatal(err)
	}
	if err := AppendTrace(path, "second line"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "first line") || !strings.Contains(string(body), "second line") {
		t.Fatalf("expected both lines preserved, got %q", body)
	}
}

// countLines is a small helper: a trace log is a plain text file, one
// occurrence per line, and every test below asserts on how many there are.
func countLines(t *testing.T, path string) int {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(body), "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

// TestAppendTraceOnceControlCase is the number the orchestrator asked for
// directly: without the fix, the same recurring cause called 20 times (the
// real machine saw one call roughly every 15 seconds, unboundedly, for as
// long as the cause held) writes 20 lines with plain AppendTrace; with the
// fix, the identical 20 calls through AppendTraceOnce write exactly 1.
func TestAppendTraceOnceControlCase(t *testing.T) {
	const calls = 20
	const cause = "rate_limits present on stdin but no -rate-limits-path is configured"

	without := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	for i := 0; i < calls; i++ {
		if err := AppendTrace(without, cause); err != nil {
			t.Fatal(err)
		}
	}
	if got := countLines(t, without); got != calls {
		t.Fatalf("plain AppendTrace: got %d lines for %d identical calls, want %d (this is the growth the fix removes)", got, calls, calls)
	}

	with := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	for i := 0; i < calls; i++ {
		if err := AppendTraceOnce(with, cause); err != nil {
			t.Fatal(err)
		}
	}
	if got := countLines(t, with); got != 1 {
		t.Fatalf("AppendTraceOnce: got %d lines for %d identical calls, want 1", got, calls)
	}
}

// TestAppendTraceOnceIsSilentWithNoIssueAndNoPriorStreak is the ordinary,
// healthy-invocation case, which must cost nothing at all -- not even an
// empty file.
func TestAppendTraceOnceIsSilentWithNoIssueAndNoPriorStreak(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	for i := 0; i < 5; i++ {
		if err := AppendTraceOnce(path, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no issue and nothing ever open must never create the log file, stat err=%v", err)
	}
}

// TestAppendTraceOnceClosesOnRecoveryWithACount is the other half of the
// orchestrator's requirement: the fact that a streak held must not be lost
// entirely, only compressed to one line, written when the cause changes.
func TestAppendTraceOnceClosesOnRecoveryWithACount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	const cause = "no -rate-limits-path configured"

	for i := 0; i < 7; i++ {
		if err := AppendTraceOnce(path, cause); err != nil {
			t.Fatal(err)
		}
	}
	if got := countLines(t, path); got != 1 {
		t.Fatalf("mid-streak: got %d lines, want 1 (still open, nothing new to say)", got)
	}

	if err := AppendTraceOnce(path, ""); err != nil { // the cause resolves
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, path); got != 2 {
		t.Fatalf("after recovery: got %d lines, want 2 (the opening line plus one closing summary)", got)
	}
	if !strings.Contains(string(body), "7 time(s)") {
		t.Fatalf("closing line does not name how many times the cause fired: %q", body)
	}
	if _, err := os.Stat(path + ".state.json"); !os.IsNotExist(err) {
		t.Fatalf("state must be cleared once the streak closes, stat err=%v", err)
	}
}

// TestAppendTraceOnceClosesTheOldCauseAndOpensTheNewOne covers a streak
// changing to a genuinely different cause without ever passing through "no
// issue" in between -- the two rate_limits problems writeRateLimitsTo can
// report (no path configured, incomplete pair) are mutually exclusive per
// call, so this is the shape a real transition between them takes.
func TestAppendTraceOnceClosesTheOldCauseAndOpensTheNewOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits_trace.log")

	if err := AppendTraceOnce(path, "cause A"); err != nil {
		t.Fatal(err)
	}
	if err := AppendTraceOnce(path, "cause A"); err != nil {
		t.Fatal(err)
	}
	if err := AppendTraceOnce(path, "cause B"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := countLines(t, path); got != 3 {
		t.Fatalf("got %d lines, want 3: cause A's opening line, its closing summary, cause B's opening line; got body %q", got, body)
	}
	if !strings.Contains(string(body), `resolved (was "cause A"`) {
		t.Fatalf("missing cause A's closing summary: %q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(string(body), "\n"), "cause B") {
		t.Fatalf("cause B's own opening line must be the last line: %q", body)
	}
}

// TestAppendTraceOnceLeavesAnUnrelatedExistingLogUntouched is the fix the
// orchestrator asked to be proven against a real, pre-existing log with a
// real history and no state file of its own (exactly the shape the
// operator's own machine was in the moment this fix was deployed): reading
// it must never error, and a healthy invocation (no issue) with nothing
// open must never append to it or otherwise change it.
func TestAppendTraceOnceLeavesAnUnrelatedExistingLogUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rate_limits_trace.log")
	var preexisting strings.Builder
	for i := 0; i < 68; i++ {
		fmt.Fprintf(&preexisting,
			"2026-09-11T13:%02d:%02d+05:00 rate_limits present on stdin but no -rate-limits-path (or statusline.rate_limits_path in config.yaml) is configured; nothing captured\n",
			17+i/60, i%60,
		)
	}
	if err := os.WriteFile(path, []byte(preexisting.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := AppendTraceOnce(path, ""); err != nil {
		t.Fatalf("AppendTraceOnce must not fail against a pre-existing log with no matching state file: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a pre-existing log must be left byte-identical when there is nothing to report: %d bytes before, %d after", len(before), len(after))
	}
	if got := countLines(t, path); got != 68 {
		t.Fatalf("got %d lines, want the original 68 preserved exactly", got)
	}
}

// --- AppendTraceOnce under real concurrent OS processes -------------------
//
// cmd/fleetdeck-status is not one long-running process: it is run fresh for
// every session's every statusline tick, and several sessions tick at once
// on a live machine (the orchestrator counted at least three, plus their
// own, while this was being written). Every one of them can call
// AppendTraceOnce against the SAME <log path>.state.json at the same
// moment. Every test above exercises AppendTraceOnce sequentially, in one
// goroutine, in one process -- none of them says anything about that.
//
// TestMain re-executes this same compiled test binary as a plain worker
// process when GO_WANT_HELPER_PROCESS is set (the standard library's own
// idiom for this, e.g. os/exec's tests) rather than adding a second
// permanent command under cmd/: this repository already has a standing
// lesson about exactly that (a throwaway probe once leaked into `make
// dist` because Make's own binary discovery does not honour Go's leading-
// underscore convention the way the toolchain itself does), and a helper
// that only exists inside this test file's own process image cannot leak
// anywhere a build could find it.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		runAppendTraceOnceHelperProcess()
		return
	}
	os.Exit(m.Run())
}

// runAppendTraceOnceHelperProcess reads logPath and message from argv
// (after the "--" testing's own flag parsing leaves in place) and makes
// exactly one AppendTraceOnce call, then exits -- the whole content of the
// worker process TestAppendTraceOnceUnderRealConcurrentProcesses spawns
// many of at once.
func runAppendTraceOnceHelperProcess() {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "helper process: want -- <logPath> <message>")
		os.Exit(2)
	}
	logPath, message := args[1], args[2]
	if err := AppendTraceOnce(logPath, message); err != nil {
		fmt.Fprintln(os.Stderr, "helper process:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// spawnAppendTraceOnce runs one real, separate OS process (this same test
// binary, re-executed) that calls AppendTraceOnce(logPath, message) and
// nothing else.
func spawnAppendTraceOnce(t *testing.T, logPath, message string) error {
	t.Helper()
	cmd := exec.Command(os.Args[0], "--", logPath, message)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// TestAcquireStateLockBreaksAStaleLock is the other half of
// acquireStateLock's own contract: a lock file left behind by a process
// that crashed mid-update (so it was never released) must not block every
// later call forever -- a permanently stuck lock is a worse failure than
// the race it exists to close.
func TestAcquireStateLockBreaksAStaleLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "rate_limits_trace.log.state.json.lock")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}

	release, ok := acquireStateLock(lockPath)
	if !ok {
		t.Fatal("a stale lock must not be honoured forever")
	}
	release()
}

// TestAcquireStateLockFailsWhenHeldAndFresh is
// TestAcquireStateLockBreaksAStaleLock's control case: a lock file that is
// genuinely fresh (another process plausibly mid-update right now) must
// not be broken, and acquiring it must fail rather than corrupt whatever
// the holder is doing.
func TestAcquireStateLockFailsWhenHeldAndFresh(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "rate_limits_trace.log.state.json.lock")
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	_, ok := acquireStateLock(lockPath)
	if ok {
		t.Fatal("a fresh, genuinely held lock must not be acquired a second time")
	}
}

// TestAppendTraceOnceUnderRealConcurrentProcesses is the check the
// orchestrator asked for by name: N real processes, the same cause, at
// once -- how many opening lines land in the log? It must be one, the same
// answer TestAppendTraceOnceControlCase already gives for N sequential
// calls in one process; this is the same property, machine-checked under
// the concurrency the real deployment actually has.
func TestAppendTraceOnceUnderRealConcurrentProcesses(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "rate_limits_trace.log")
	const n = 50
	const message = "concurrent cause"

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := spawnAppendTraceOnce(t, logPath, message); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("helper process failed: %v", err)
	}

	got := countLines(t, logPath)
	t.Logf("%d concurrent real processes, same cause: %d opening line(s) in the log (want 1)", n, got)
	if got != 1 {
		t.Fatalf("%d concurrent processes with the same cause produced %d opening lines, want exactly 1 -- the read-check-write on the state file is not safe under real concurrency", n, got)
	}
}
