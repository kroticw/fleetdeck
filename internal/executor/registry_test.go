package executor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRegistryRoutesKeysWithoutNativeIDGuessing(t *testing.T) {
	r, err := NewRegistry(Adapter{ID: Claude}, Adapter{ID: Codex})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		raw       string
		id        ID
		canonical Key
	}{
		{"ABC12345", Claude, "ABC12345"},
		{"codex:01A1A6D4-90A4-70F3-9130-A59EC873D0BA", Codex, "codex:01a1a6d4-90a4-70f3-9130-a59ec873d0ba"},
	} {
		a, key, err := r.ForKey(tt.raw)
		if err != nil || a.ID != tt.id || key != tt.canonical {
			t.Fatalf("%q: adapter=%+v key=%q err=%v", tt.raw, a, key, err)
		}
	}
	if _, _, err := r.ForKey("other:abc12345"); !errors.Is(err, ErrInvalidKey) {
		t.Fatal(err)
	}
}

func TestRegistryRejectsDuplicateAndUnknownAdapters(t *testing.T) {
	if _, err := NewRegistry(Adapter{ID: Claude}, Adapter{ID: Claude}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err := NewRegistry(Adapter{ID: "other"}); !errors.Is(err, ErrUnknownExecutor) {
		t.Fatal(err)
	}
	r, err := NewRegistry(Adapter{ID: Claude})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ForID(Codex); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := r.ForID(""); !errors.Is(err, ErrUnknownExecutor) {
		t.Fatal("registry must not silently register the legacy default")
	}
}

func TestUnobservedMetricsAreAbsent(t *testing.T) {
	raw, err := json.Marshal(Session{Key: "abc12345", Executor: Claude, NeedsHuman: Unknown})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"context", "cost", "model", "limits"} {
		if _, exists := fields[field]; exists {
			t.Fatalf("unobserved %s exposed as a value: %s", field, raw)
		}
	}
	if !strings.Contains(string(raw), `"needsHuman":"unknown"`) {
		t.Fatalf("unknown reported as known: %s", raw)
	}
}

func TestVerdictSurvivesAJSONRoundTrip(t *testing.T) {
	for _, v := range []Verdict{Unknown, No, Yes} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var back Verdict
		if err := json.Unmarshal(raw, &back); err != nil || back != v {
			t.Fatalf("%q came back as %q (%v)", v, back, err)
		}
	}
	var odd Verdict
	if err := json.Unmarshal([]byte(`"maybe"`), &odd); err != nil || odd != Unknown {
		t.Fatalf("unrecognized verdict must read as unknown, got %q %v", odd, err)
	}
}
