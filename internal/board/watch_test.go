package board

import (
	"context"
	"os"
	"testing"
	"time"
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
		time.Sleep(10 * time.Millisecond)
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
