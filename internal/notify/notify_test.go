package notify

import (
	"errors"
	"testing"
)

func TestFireSendsOncePerKey(t *testing.T) {
	sent := 0
	n := New(func(title, text string) error { sent++; return nil })
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
	n := New(func(title, text string) error { sent++; return nil })
	n.Fire("k", "t", "x")
	n.Clear("k")
	n.Fire("k", "t", "x")
	if sent != 2 {
		t.Fatalf("a state that went away and came back is a new event, got %d", sent)
	}
}

func TestSendFailureIsReportedAndNotRemembered(t *testing.T) {
	fail := true
	n := New(func(title, text string) error {
		if fail {
			return errors.New("osascript missing")
		}
		return nil
	})
	if err := n.Fire("k", "t", "x"); err == nil {
		t.Fatal("a failed banner must be reported")
	}
	fail = false
	if err := n.Fire("k", "t", "x"); err != nil {
		t.Fatal("a key whose banner failed must be retried, not marked as delivered")
	}
}
