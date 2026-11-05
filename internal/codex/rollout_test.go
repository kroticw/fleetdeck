package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A runtime that reconnected, or one of an older build, reports no context
// until the next turn; the rollout Codex writes has the last reading (T-216).
func TestTheRolloutGivesTheLastContextReading(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "11", "05")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session_meta","payload":{"id":"01a1"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":1000},"model_context_window":475000}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":171829},"model_context_window":475000}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":null}}`,
		`{"type":"response_item","payload":{"type":"message","content":"` + strings.Repeat("x", 300000) + `"}}`,
	}
	path := filepath.Join(dir, "rollout-2026-11-05T11-45-43-01a1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, ok := ReadRollout(home, "01a1")
	if !ok || r.Tokens != 171829 || r.Window != 475000 || r.WrittenAt.IsZero() {
		t.Fatalf("reading = %+v, %v", r, ok)
	}
	if _, ok := ReadRollout(home, "other"); ok {
		t.Fatal("a thread with no rollout has a reading")
	}
	if _, ok := ReadRollout(home, "*"); ok {
		t.Fatal("a pattern for a thread id reads some rollout")
	}
}
