// Package notify shows macOS banners. The decision to notify belongs to the
// server, not the browser: the server knows the state and a banner must not
// depend on whether a tab is open.
package notify

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

type Notifier struct {
	send func(title, text string) error

	mu   sync.Mutex
	seen map[string]bool
}

func New(send func(title, text string) error) *Notifier {
	return &Notifier{send: send, seen: map[string]bool{}}
}

// Fire delivers a banner the first time a key appears. A key is an event, not a
// state: while a session stands waiting, one banner is enough.
func (n *Notifier) Fire(key, title, text string) error {
	n.mu.Lock()
	already := n.seen[key]
	n.mu.Unlock()
	if already {
		return nil
	}
	if err := n.send(title, text); err != nil {
		return fmt.Errorf("send banner %s: %w", key, err)
	}
	n.mu.Lock()
	n.seen[key] = true
	n.mu.Unlock()
	return nil
}

// Clear forgets a key so its next appearance is a new event.
func (n *Notifier) Clear(key string) {
	n.mu.Lock()
	delete(n.seen, key)
	n.mu.Unlock()
}

// OSAScriptSend shows a macOS notification banner.
func OSAScriptSend(title, text string) error {
	esc := func(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, esc(text), esc(title))
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("osascript: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
