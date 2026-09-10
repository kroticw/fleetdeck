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
	defer n.mu.Unlock()
	if n.seen[key] {
		return nil
	}
	if err := n.send(title, text); err != nil {
		return fmt.Errorf("send banner %s: %w", key, err)
	}
	n.seen[key] = true
	return nil
}

// Clear forgets a key so its next appearance is a new event.
func (n *Notifier) Clear(key string) {
	n.mu.Lock()
	delete(n.seen, key)
	n.mu.Unlock()
}

// escapeAppleScriptString escapes a string for use in AppleScript double-quoted strings.
// It escapes backslashes first, then quotes, to prevent escape-sequence injection.
func escapeAppleScriptString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return r.Replace(s)
}

// OSAScriptSend shows a macOS notification banner.
func OSAScriptSend(title, text string) error {
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, escapeAppleScriptString(text), escapeAppleScriptString(title))
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("osascript: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
