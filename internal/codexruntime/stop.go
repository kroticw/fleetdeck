package codexruntime

import (
	"context"
	"errors"
	"time"
)

const stopWait = 10 * time.Second

var ErrWorkSurvived = errors.New("codex work did not stop")

// stopWork interrupts the thread's running turns and cleans its background
// processes, then proves both are gone. Closing the TUI alone stops neither.
func stopWork(ctx context.Context, native *nativeControl, thread string) error {
	ctx, cancel := context.WithTimeout(ctx, stopWait)
	defer cancel()
	active, err := activeTurns(ctx, native, thread)
	if err != nil {
		return err
	}
	for _, turn := range active {
		if err := native.call(ctx, "turn/interrupt", map[string]string{"threadId": thread, "turnId": turn}, nil); err != nil {
			return err
		}
	}
	// Interrupt is asynchronous; a process started by the ending turn could
	// otherwise appear after the cleanup.
	if err := until(ctx, func() (bool, error) {
		active, err := activeTurns(ctx, native, thread)
		return len(active) == 0, err
	}); err != nil {
		return err
	}
	if err := native.call(ctx, "thread/backgroundTerminals/clean", map[string]string{"threadId": thread}, nil); err != nil {
		return err
	}
	return until(ctx, func() (bool, error) {
		n, err := backgroundTerminals(ctx, native, thread)
		return n == 0, err
	})
}

func activeTurns(ctx context.Context, native *nativeControl, thread string) ([]string, error) {
	var page struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := native.call(ctx, "thread/turns/list", map[string]any{"threadId": thread, "itemsView": "notLoaded", "limit": 10}, &page); err != nil {
		return nil, err
	}
	var active []string
	for _, turn := range page.Data {
		if turn.Status == "inProgress" {
			active = append(active, turn.ID)
		}
	}
	return active, nil
}

func backgroundTerminals(ctx context.Context, native *nativeControl, thread string) (int, error) {
	var page struct {
		Data       *[]struct{} `json:"data"`
		NextCursor *string     `json:"nextCursor"`
	}
	if err := native.call(ctx, "thread/backgroundTerminals/list", map[string]string{"threadId": thread}, &page); err != nil {
		return 0, err
	}
	// A missing list is no evidence of absence.
	if page.Data == nil {
		return 0, ErrInvalidFrame
	}
	n := len(*page.Data)
	if page.NextCursor != nil && *page.NextCursor != "" {
		n++
	}
	return n, nil
}

func until(ctx context.Context, done func() (bool, error)) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := done()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrWorkSurvived
		case <-ticker.C:
		}
	}
}
