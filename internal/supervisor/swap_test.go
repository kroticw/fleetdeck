package supervisor

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func bundle(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
}

func versionAt(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "version"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSwapPutsTheStagedBundleAtTheCanonicalPathAndTheOldOneAside(t *testing.T) {
	root := t.TempDir()
	canonical, staged := filepath.Join(root, "fleetdeck.app"), filepath.Join(root, ".staged.app")
	bundle(t, canonical, "old")
	bundle(t, staged, "new")

	if err := Swap(staged, canonical); err != nil {
		t.Fatal(err)
	}
	if got := versionAt(t, canonical); got != "new" {
		t.Fatalf("canonical holds %q, want the staged bundle", got)
	}
	if got := versionAt(t, staged); got != "old" {
		t.Fatalf("the staged path holds %q, want the old bundle kept aside", got)
	}
}

// A first install has nothing at the canonical path to protect: the staged
// bundle simply moves there.
func TestSwapOntoNothingMovesTheStagedBundleIn(t *testing.T) {
	root := t.TempDir()
	canonical, staged := filepath.Join(root, "fleetdeck.app"), filepath.Join(root, ".staged.app")
	bundle(t, staged, "new")

	if err := Swap(staged, canonical); err != nil {
		t.Fatal(err)
	}
	if got := versionAt(t, canonical); got != "new" {
		t.Fatalf("canonical holds %q, want the staged bundle", got)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("the staged path still exists after a move: %v", err)
	}
}

// hammer reads the canonical bundle over and over while swap runs rounds
// times, and counts the reads that found nothing there.
func hammer(t *testing.T, canonical, staged string, rounds int, swap func(staged, canonical string) error) (misses, reads int64) {
	t.Helper()
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if _, err := os.ReadFile(filepath.Join(canonical, "version")); err != nil {
				atomic.AddInt64(&misses, 1)
			}
			atomic.AddInt64(&reads, 1)
		}
	}()
	for i := 0; i < rounds; i++ {
		if err := swap(staged, canonical); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("swap %d: %v", i, err)
		}
	}
	stop.Store(true)
	wg.Wait()
	return misses, reads
}

// naiveSwap is the way an update would do it without an atomic exchange:
// the old bundle out of the way, then the new one in. Between the two there
// is nothing at the canonical path.
func naiveSwap(staged, canonical string) error {
	aside := canonical + ".aside"
	if err := os.Rename(canonical, aside); err != nil {
		return err
	}
	if err := os.Rename(staged, canonical); err != nil {
		return err
	}
	return os.Rename(aside, staged)
}

// The operator's invariant: «на каноническом пути всегда лежит рабочая
// версия, в любой момент». A reader that looks at the canonical bundle while
// it is being replaced must never find it missing.
func TestTheCanonicalPathIsNeverEmptyDuringASwap(t *testing.T) {
	const rounds = 2000
	root := t.TempDir()
	canonical, staged := filepath.Join(root, "fleetdeck.app"), filepath.Join(root, ".staged.app")
	bundle(t, canonical, "a")
	bundle(t, staged, "b")

	// Control: the same reader does see the gap a two-step replacement
	// leaves. Without this, zero misses below could mean the reader is too
	// slow to catch any gap at all.
	misses, reads := hammer(t, canonical, staged, rounds, naiveSwap)
	if misses == 0 {
		t.Fatalf("the reader found no gap in %d reads across %d two-step replacements: it cannot see what this test is about", reads, rounds)
	}
	t.Logf("control: %d of %d reads missed during two-step replacements", misses, reads)

	misses, reads = hammer(t, canonical, staged, rounds, Swap)
	if misses != 0 {
		t.Fatalf("%d of %d reads found nothing at the canonical path during Swap", misses, reads)
	}
	t.Logf("Swap: 0 of %d reads missed", reads)
}
