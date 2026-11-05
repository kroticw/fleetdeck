package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		`{"timestamp":"2026-11-05T07:19:27.919Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":171829},"model_context_window":475000},"rate_limits":{"primary":{"used_percent":22.0,"window_minutes":300,"resets_at":1793879154},"secondary":{"used_percent":19.0,"window_minutes":10080,"resets_at":1794408872}}}}`,
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
	if len(r.Limits) != 2 || r.Limits[0].Minutes != 300 || r.Limits[0].UsedPercent != 22 || r.Limits[1].Minutes != 10080 ||
		r.Limits[1].ResetsAt == nil || r.Limits[1].ResetsAt.Unix() != 1794408872 || r.LimitsAt.Format(time.RFC3339) != "2026-11-05T07:19:27Z" {
		t.Fatalf("limits = %+v at %v", r.Limits, r.LimitsAt)
	}
	if _, ok := ReadRollout(home, "other"); ok {
		t.Fatal("a thread with no rollout has a reading")
	}
	if _, ok := ReadRollout(home, "*"); ok {
		t.Fatal("a pattern for a thread id reads some rollout")
	}
}
