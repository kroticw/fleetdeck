package board

import (
	"context"
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch calls onChange when anything in dir changes, coalescing bursts into one
// call per 300ms so a rewrite by an agent does not flood the panel.
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
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-w.Events:
			if !ok {
				return nil
			}
			if timer == nil {
				timer = time.AfterFunc(300*time.Millisecond, onChange)
			} else {
				timer.Reset(300 * time.Millisecond)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			return fmt.Errorf("watcher: %w", err)
		}
	}
}
