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
