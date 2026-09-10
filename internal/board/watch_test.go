package board

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestWatchFiresOnChangeAndCoalesces(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fired := make(chan struct{}, 16)
	go Watch(ctx, dir, func() { fired <- struct{}{} })
	time.Sleep(100 * time.Millisecond)

	for i := 0; i < 5; i++ {
		writeCard(t, dir, "c.md", sample)
	}

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("a change in the board directory must wake the watcher")
	}
	time.Sleep(500 * time.Millisecond)
	if len(fired) > 1 {
		t.Fatalf("five rapid writes must coalesce, got %d extra callbacks", len(fired))
	}
}

func TestWatchOnMissingDirectoryIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Watch(ctx, "/definitely/not/here", func() {}); err == nil {
		t.Fatal("watching a directory that does not exist must fail, not sit silent")
	}
}

// TestWatchDoesNotCallOnChangeAfterReturning rules out the case where
// case <-ctx.Done(): return nil leaves a pending coalescing timer alive: for
// up to coalesceWindow after Watch has returned, the caller's onChange would
// otherwise still fire against state the caller has already torn down.
func TestWatchDoesNotCallOnChangeAfterReturning(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	var mu sync.Mutex
	returned := false
	calledAfterReturn := false

	done := make(chan struct{})
	go func() {
		_ = Watch(ctx, dir, func() {
			mu.Lock()
			if returned {
				calledAfterReturn = true
			}
			mu.Unlock()
		})
		mu.Lock()
		returned = true
		mu.Unlock()
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)

	// Fire one event so the coalescing timer gets armed, give fsnotify a
	// moment to deliver it, then cancel — the timer must not survive Watch's
	// return.
	writeCard(t, dir, "c.md", sample)
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after its context was cancelled")
	}

	// coalesceWindow is 300ms; wait comfortably past it.
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calledAfterReturn {
		t.Fatal("onChange must not run after Watch has returned")
	}
}

// TestWatchOnDirectoryRemovedWhileWatchingIsAnError rules out the failure
// TestWatchOnMissingDirectoryIsAnError only catches at startup: a directory
// that is removed or renamed after Watch has already attached must not leave
// the loop blocked forever waiting on a watch the kernel already tore down.
func TestWatchOnDirectoryRemovedWhileWatchingIsAnError(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- Watch(ctx, dir, func() {}) }()
	time.Sleep(100 * time.Millisecond)

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove watched directory: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Watch must return an error when the watched directory is removed, not sit silent")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch blocked instead of returning an error after its directory was removed")
	}
}

// TestIsRecoverableTreatsOverflowAsTransient documents which watcher errors
// Watch treats as a resync signal rather than a fatal condition.
// fsnotify.ErrEventOverflow (a Linux CI leg's kernel queue overflow) must not
// kill the watcher permanently.
func TestIsRecoverableTreatsOverflowAsTransient(t *testing.T) {
	if !isRecoverable(fsnotify.ErrEventOverflow) {
		t.Fatal("fsnotify.ErrEventOverflow must be treated as recoverable")
	}
	if isRecoverable(errors.New("some other watcher error")) {
		t.Fatal("an arbitrary error must not be treated as recoverable")
	}
}

// TestWatchAttachesToRealBoardDirectory points the watcher at a real, human-maintained
// board directory instead of one the test just created. It asserts only that the
// watcher attaches and shuts down cleanly — it never writes to that directory, which
// belongs to the operator and to other agents. A watcher that cannot attach to the
// real vault path would otherwise be discovered by a person, not by the suite.
func TestWatchAttachesToRealBoardDirectory(t *testing.T) {
	dir := os.Getenv("FLEETDECK_SMOKE_BOARD_DIR")
	if dir == "" {
		t.Skip("set FLEETDECK_SMOKE_BOARD_DIR to a real board cards directory to run this")
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Watch(ctx, dir, func() {}) }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("watching the real board directory %s failed: %v", dir, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after its context was cancelled")
	}
}
