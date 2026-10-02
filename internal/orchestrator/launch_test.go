package orchestrator

import (
	"slices"
	"testing"
)

// A worker is started with every one of its three settings spelled out, the
// defaults included: a flag left out is a value taken from the operator's own
// settings file, which is how workers came up on sonnet, in acceptEdits and
// inside the sandbox (T-061).
func TestLaunchArgsSpellOutTheDefaults(t *testing.T) {
	want := []string{"--model", "opus", "--permission-mode", "auto", "--settings", `{"sandbox":{"enabled":false}}`}
	if got := (Launch{}).Args(); !slices.Equal(got, want) {
		t.Fatalf("Args() = %q, want %q", got, want)
	}
}

func TestLaunchArgsCarryWhatWasConfigured(t *testing.T) {
	got := Launch{Model: "claude-fable-5", PermissionMode: "acceptEdits", Sandbox: true}.Args()
	want := []string{"--model", "claude-fable-5", "--permission-mode", "acceptEdits", "--settings", `{"sandbox":{"enabled":true}}`}
	if !slices.Equal(got, want) {
		t.Fatalf("Args() = %q, want %q", got, want)
	}
}
