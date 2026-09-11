package usage

import (
	"errors"
	"fmt"
	"os"
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
