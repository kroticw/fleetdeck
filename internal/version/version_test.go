package version

import "testing"

func TestStringFallsBackWhenUnset(t *testing.T) {
	value = ""
	if got := String(); got != "dev" {
		t.Fatalf("want dev for unset build version, got %q", got)
	}
}

func TestStringUsesInjectedValue(t *testing.T) {
	value = "1.2.3"
	defer func() { value = "" }()
	if got := String(); got != "1.2.3" {
		t.Fatalf("want injected version, got %q", got)
	}
}
