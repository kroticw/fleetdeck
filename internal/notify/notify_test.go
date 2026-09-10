package notify

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEscapeAppleScriptString(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic cases
		{"hello", "hello"},
		{"hello world", "hello world"},
		// Quote escaping
		{`say "hi"`, `say \"hi\"`},
		// Backslash escaping
		{"path\\to\\file", "path\\\\to\\\\file"},
		// The injection vector: backslash-quote must become escaped-backslash-escaped-quote
		{"test\\\"", "test\\\\\\\""},
		// Single backslash
		{"\\", "\\\\"},
		// Single quote
		{"\"", "\\\""},
	}
	for _, tt := range tests {
		got := escapeAppleScriptString(tt.input)
		if got != tt.expected {
			t.Errorf("escapeAppleScriptString(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFireSendsOncePerKey(t *testing.T) {
	sent := 0
	n := New(func(_, _ string) error { sent++; return nil })
	for i := 0; i < 3; i++ {
		if err := n.Fire("session:abc:waiting", "t", "x"); err != nil {
			t.Fatal(err)
		}
	}
	if sent != 1 {
		t.Fatalf("a standing state must produce one banner, got %d", sent)
	}
}

func TestFireAgainAfterClear(t *testing.T) {
	sent := 0
	n := New(func(_, _ string) error { sent++; return nil })
	n.Fire("k", "t", "x")
	n.Clear("k")
	n.Fire("k", "t", "x")
	if sent != 2 {
		t.Fatalf("a state that went away and came back is a new event, got %d", sent)
	}
}

func TestSendFailureIsReportedAndNotRemembered(t *testing.T) {
	fail := true
	sendCount := 0
	n := New(func(_, _ string) error {
		sendCount++
		if fail {
			return errors.New("osascript missing")
		}
		return nil
	})
	if err := n.Fire("k", "t", "x"); err == nil {
		t.Fatal("a failed banner must be reported")
	}
	if sendCount != 1 {
		t.Fatalf("first Fire must call send exactly once, got %d", sendCount)
	}
	fail = false
	if err := n.Fire("k", "t", "x"); err != nil {
		t.Fatal("a key whose banner failed must be retried, not marked as delivered")
	}
	if sendCount != 2 {
		t.Fatalf("second Fire must call send again (retry), got %d total sends", sendCount)
	}
}

func TestFireConcurrentSameSendsOnce(t *testing.T) {
	sendCount := int64(0)
	n := New(func(_, _ string) error {
		atomic.AddInt64(&sendCount, 1)
		// Simulate osascript latency to expose race condition without proper locking
		time.Sleep(5 * time.Millisecond)
		return nil
	})

	const numGoroutines = 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			if err := n.Fire("same-key", "title", "text"); err != nil {
				t.Error(err)
			}
		}()
	}

	wg.Wait()

	if atomic.LoadInt64(&sendCount) != 1 {
		t.Fatalf("concurrent Fire calls with same key must result in exactly one send, got %d", sendCount)
	}
}
