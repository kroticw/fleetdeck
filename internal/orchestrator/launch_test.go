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

// Effort is given only when set: claude's own default is not frozen here.
func TestEffortIsPassedOnlyWhenSet(t *testing.T) {
	if got := (Launch{}).Args(); slices.Contains(got, "--effort") {
		t.Fatalf("Args() = %q gives an effort nobody chose", got)
	}
	got := Launch{Effort: "xhigh"}.Args()
	if i := slices.Index(got, "--effort"); i < 0 || got[i+1] != "xhigh" {
		t.Fatalf("Args() = %q", got)
	}
}
