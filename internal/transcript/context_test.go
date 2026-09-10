package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContextUsageSumsLastAssistantUsage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "u.jsonl")
	line := `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":2,"cache_creation_input_tokens":984,"cache_read_input_tokens":597051}}}`
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ContextUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tokens != 598037 {
		t.Fatalf("want 598037 tokens, got %d", got.Tokens)
	}
	if got.Window != 1000000 {
		t.Fatalf("opus-5 window must be 1M, got %d", got.Window)
	}
	if !got.Estimated {
		t.Fatal("a value derived from the transcript must be marked as an estimate")
	}
}

func TestContextUsageWithoutUsageIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nousage.jsonl")
	if err := os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ContextUsage(p); err == nil {
		t.Fatal("a transcript with no usage data must fail, not report zero context")
	}
}

func TestContextUsageUnknownModelFallsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "unknown.jsonl")
	line := `{"type":"assistant","message":{"model":"future-model-9","usage":{"input_tokens":10,"cache_read_input_tokens":90}}}`
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ContextUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Window != 200000 {
		t.Fatalf("unknown model must fall back to 200k, got %d", got.Window)
	}
}

// TestContextUsageSkipsSyntheticZeroUsage guards against the bug where a
// trailing synthetic entry (an interruption or an API error, model
// "<synthetic>", usage fully populated but all zero) made ContextUsage stop
// at that line and report an empty context for a session that was actually
// nearly full. Break it by reverting the skip in context.go's visit closure
// and this test fails with 0 tokens instead of 598037.
func TestContextUsageSkipsSyntheticZeroUsage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "synthetic-tail.jsonl")
	realLine := `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":2,"cache_creation_input_tokens":244,"cache_read_input_tokens":599992}}}`
	synthetic := `{"type":"assistant","message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	if err := os.WriteFile(p, []byte(realLine+"\n"+synthetic+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ContextUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tokens != 600238 {
		t.Fatalf("want the real entry's 600238 tokens, got %d", got.Tokens)
	}
	if got.Window != 1000000 {
		t.Fatalf("want opus-5 window, got %d", got.Window)
	}
}

// TestContextUsageFableWindow guards the missing table entry: real
// transcripts carry model "claude-fable-5" (distinct from
// "claude-fable-5-1"), and a miss used to fall back to 200k, understating a
// session's actual headroom. Break it by deleting the "claude-fable-5" entry
// from the windows map and this test fails on the 200k fallback.
func TestContextUsageFableWindow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fable.jsonl")
	line := `{"type":"assistant","message":{"model":"claude-fable-5","usage":{"input_tokens":1,"cache_read_input_tokens":1}}}`
	if err := os.WriteFile(p, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ContextUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Window != 1000000 {
		t.Fatalf("claude-fable-5 window must be 1M, got %d", got.Window)
	}
}

func TestContextUsageUsesLastNotFirst(t *testing.T) {
	p := filepath.Join(t.TempDir(), "multi.jsonl")
	first := `{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":1,"cache_read_input_tokens":1}}}`
	last := `{"type":"assistant","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":50,"cache_read_input_tokens":50}}}`
	if err := os.WriteFile(p, []byte(first+"\n"+last+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ContextUsage(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tokens != 100 {
		t.Fatalf("want the last line's usage (100 tokens), got %d", got.Tokens)
	}
	if got.Window != 200000 {
		t.Fatalf("want the last line's model window (haiku, 200k), got %d", got.Window)
	}
}
