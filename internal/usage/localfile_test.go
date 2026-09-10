package usage

import (
	"errors"
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
