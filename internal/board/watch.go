package board

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
)

// coalesceWindow is how long Watch waits after the last observed change
// before calling onChange, so a burst of writes collapses into one call.
const coalesceWindow = 300 * time.Millisecond

// Watch calls onChange when anything changes directly in dir, coalescing
// bursts into one call per coalesceWindow so a rewrite by an agent does not
// flood the panel. The watch is not recursive: fsnotify.Add watches dir
// itself, not its subdirectories, so changes inside a subdirectory of dir
// are never seen.
//
// Watch reports failure rather than sitting silent: it returns an error if
// dir cannot be watched at all, and it also returns an error if the watched
// directory is later removed or renamed out from under it, instead of
// blocking forever with nothing left to watch. A transient
// fsnotify.ErrEventOverflow is treated as an ordinary change rather than a
// fatal error, since it only means some events were dropped, not that
// watching has stopped working.
func Watch(ctx context.Context, dir string, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer func() { _ = w.Close() }()
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}

	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Name == dir && (ev.Op.Has(fsnotify.Remove) || ev.Op.Has(fsnotify.Rename)) {
				return fmt.Errorf("watched directory %s was removed or renamed", dir)
			}
			if timer == nil {
				timer = time.AfterFunc(coalesceWindow, onChange)
			} else {
				timer.Reset(coalesceWindow)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			if isRecoverable(err) {
				if timer == nil {
					timer = time.AfterFunc(coalesceWindow, onChange)
				} else {
					timer.Reset(coalesceWindow)
				}
				continue
			}
			return fmt.Errorf("watcher: %w", err)
		}
	}
}

// isRecoverable reports whether err received from the watcher's error
// channel should be treated as a transient hiccup instead of a fatal
// condition. fsnotify.ErrEventOverflow means the kernel's event queue
// dropped events — a signal to resync and keep going, not a reason to stop
// watching.
func isRecoverable(err error) bool {
	return errors.Is(err, fsnotify.ErrEventOverflow)
}
