package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTheOldWindowSeesEveryStepTheNewOneReportsInOrder(t *testing.T) {
	h := Handover{Path: filepath.Join(t.TempDir(), "handover")}
	go func() {
		for _, s := range []Step{StepAlive, StepPanel, StepSwapped, StepDone} {
			time.Sleep(20 * time.Millisecond)
			if err := h.Report(s, "detail of "+string(s)); err != nil {
				t.Error(err)
			}
		}
	}()

	var mu sync.Mutex
	var seen []Step
	last, detail, err := h.Watch(context.Background(), func(s Step, _ string) {
		mu.Lock()
		seen = append(seen, s)
		mu.Unlock()
	})
	if err != nil || last != StepDone || detail != "detail of done" {
		t.Fatalf("Watch = %s, %q, %v; want done", last, detail, err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []Step{StepAlive, StepPanel, StepSwapped, StepDone}
	if len(seen) != len(want) {
		t.Fatalf("seen %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("seen %v, want %v", seen, want)
		}
	}
}

func TestAFailureEndsTheWatchWithItsReason(t *testing.T) {
	h := Handover{Path: filepath.Join(t.TempDir(), "handover")}
	if err := h.Report(StepAlive, ""); err != nil {
		t.Fatal(err)
	}
	if err := h.Report(StepFailed, "the new panel did not answer within 330ms"); err != nil {
		t.Fatal(err)
	}
	last, detail, err := h.Watch(context.Background(), nil)
	if err != nil || last != StepFailed || detail != "the new panel did not answer within 330ms" {
		t.Fatalf("Watch = %s, %q, %v; want the failure and its reason", last, detail, err)
	}
}

// A new window that never reports anything must not keep the old one waiting
// past its deadline.
func TestAWatchEndsWithItsDeadline(t *testing.T) {
	h := Handover{Path: filepath.Join(t.TempDir(), "handover")}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	last, _, err := h.Watch(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) || last != "" {
		t.Fatalf("Watch = %q, %v; want the deadline", last, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Watch ignored its deadline")
	}
}

// A line still being written is not a step: the reader never acts on half of
// "swapped".
func TestAHalfWrittenLineIsNotReadAsAStep(t *testing.T) {
	h := Handover{Path: filepath.Join(t.TempDir(), "handover")}
	if err := os.WriteFile(h.Path, []byte("alive\t\nswap"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var seen []Step
	_, _, _ = h.Watch(ctx, func(s Step, _ string) { seen = append(seen, s) })
	if len(seen) != 1 || seen[0] != StepAlive {
		t.Fatalf("seen %v, want only the complete line", seen)
	}
}
