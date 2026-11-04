package executor

import (
	"errors"
	"testing"
)

func TestParseKeyPreservesLegacyClaude(t *testing.T) {
	for _, raw := range []string{"abc123", "ABC12345", "123456abcdef"} {
		id, key, err := ParseKey(raw)
		if err != nil || id != Claude || string(key) != raw {
			t.Fatalf("ParseKey(%q)=(%q,%q,%v)", raw, id, key, err)
		}
	}
}
func TestParseKeyNormalizesCodexUUID(t *testing.T) {
	id, key, err := ParseKey("codex:01A1A6D4-90A4-70F3-9130-A59EC873D0BA")
	if err != nil || id != Codex || key != "codex:01a1a6d4-90a4-70f3-9130-a59ec873d0ba" {
		t.Fatalf("got %q %q %v", id, key, err)
	}
}
func TestParseKeyRejectsAmbiguousAndUnsafeValues(t *testing.T) {
	for _, raw := range []string{"", "abc12", "abc1234567890", "not-an-id", "unknown:abc123", "claude:abc123", "codex:abc123", "codex:01a1a6d4/90a4-70f3-9130-a59ec873d0ba", "codex:01a1a6d4-90a4-70f3-9130-a59ec873d0bz", " abc123", "abc123\n"} {
		if _, _, err := ParseKey(raw); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("accepted %q: %v", raw, err)
		}
	}
}
func TestParseIDDefaultsOnlyMissingExecutorToClaude(t *testing.T) {
	for _, raw := range []string{"", "claude", "codex"} {
		id, err := ParseID(raw)
		if err != nil || id == "" {
			t.Fatalf("ParseID(%q)=%q %v", raw, id, err)
		}
	}
	for _, raw := range []string{"other", "CODEX", " codex"} {
		if _, err := ParseID(raw); !errors.Is(err, ErrUnknownExecutor) {
			t.Errorf("accepted %q: %v", raw, err)
		}
	}
}
