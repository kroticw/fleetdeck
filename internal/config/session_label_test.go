package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	uuidA = "11111111-1111-1111-1111-111111111111"
	uuidB = "22222222-2222-2222-2222-222222222222"
)

// TestSetSessionLabelInsertsIntoAFreshlySavedConfig covers the shape every
// real config starts in: Save always writes session_labels (see
// configToFile), but as the empty flow-style "session_labels: {}" — verified
// against Save's actual output, not assumed — and the first-ever label must
// still land correctly, and must not disturb any other section.
func TestSetSessionLabelInsertsIntoAFreshlySavedConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	cfg.BoardPath = "/my/board"
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "session_labels: {}") {
		t.Fatalf("test assumption broken: Save no longer writes an empty flow-style map, got:\n%s", before)
	}

	if err := SetSessionLabel(p, uuidA, "orchestrator"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("the written file must still parse: %v", err)
	}
	if got.SessionLabels[uuidA] != "orchestrator" {
		t.Fatalf("SessionLabels[uuidA] = %q, want orchestrator", got.SessionLabels[uuidA])
	}
	if got.BoardPath != "/my/board" {
		t.Fatalf("an untouched sibling field must survive, got BoardPath=%q", got.BoardPath)
	}
}

// TestSetSessionLabelAddsASecondEntryBesideTheFirst covers the non-empty
// path: an existing entry's indentation is copied, not reinvented, and both
// entries survive.
func TestSetSessionLabelAddsASecondEntryBesideTheFirst(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionLabel(p, uuidA, "first"); err != nil {
		t.Fatalf("first SetSessionLabel: %v", err)
	}
	if err := SetSessionLabel(p, uuidB, "second"); err != nil {
		t.Fatalf("second SetSessionLabel: %v", err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("the written file must still parse: %v", err)
	}
	if got.SessionLabels[uuidA] != "first" || got.SessionLabels[uuidB] != "second" {
		t.Fatalf("want both entries, got %+v", got.SessionLabels)
	}
}

// TestSetSessionLabelAddsASecondEntryUsingTheFirstEntrysIndent pins the
// "copy a sibling's indent" behavior on a file whose session_labels entries
// are NOT at Save's own 4-space default — a hand-edited file this package
// must still respect. Checking only the parsed map (as
// TestSetSessionLabelAddsASecondEntryBesideTheFirst does) cannot catch a
// wrong indent: yaml parses either way. Only reading the raw line proves the
// new entry actually landed at the existing entry's own indent rather than
// a hardcoded default.
func TestSetSessionLabelAddsASecondEntryUsingTheFirstEntrysIndent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "session_labels:\n  " + uuidA + ": first\n" +
		"server:\n  port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidB, "second"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\n  "+uuidB+": second\n") {
		t.Fatalf("the new entry must be indented 2 spaces, matching its sibling, got:\n%s", raw)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("the written file must still parse: %v", err)
	}
	if got.SessionLabels[uuidA] != "first" || got.SessionLabels[uuidB] != "second" {
		t.Fatalf("want both entries, got %+v", got.SessionLabels)
	}
}

// TestSetSessionLabelPreservesCommentsWhenUpdating is the operator's own
// acceptance test, carried over from SetField: a comment above the entry and
// a trailing comment on the entry's own line both survive a value change.
func TestSetSessionLabelPreservesCommentsWhenUpdating(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "board:\n    path: \"\"\n" +
		"session_labels:\n" +
		"    # pinned by hand while triaging the incident\n" +
		"    " + uuidA + ": old-name  # do not remove\n" +
		"server:\n    port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, "new-name"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "# pinned by hand while triaging the incident") {
		t.Fatalf("the comment above the entry must survive, got:\n%s", got)
	}
	if !strings.Contains(got, "# do not remove") {
		t.Fatalf("the trailing comment must survive, got:\n%s", got)
	}
	if !strings.Contains(got, uuidA+": new-name") {
		t.Fatalf("the value must actually change, got:\n%s", got)
	}
	if strings.Contains(got, "old-name") {
		t.Fatalf("the old value must not remain anywhere, got:\n%s", got)
	}
	if !strings.Contains(got, "port: 7777") {
		t.Fatalf("an untouched sibling section must survive byte for byte, got:\n%s", got)
	}
}

// TestSetSessionLabelEmptyRemovesTheEntry pins the rule the route documents:
// an empty label means "forget this session's label", not "store an empty
// string" — a config that only ever gained entries would eventually carry
// hand-written labels for sessions nobody remembers.
func TestSetSessionLabelEmptyRemovesTheEntry(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionLabel(p, uuidA, "temporary"); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, ""); err != nil {
		t.Fatalf("SetSessionLabel(\"\"): %v", err)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.SessionLabels[uuidA]; ok {
		t.Fatalf("want the entry gone entirely, not stored as an empty string, got %+v", got.SessionLabels)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), uuidA) {
		t.Fatalf("the removed session's UUID must not remain anywhere in the file, got:\n%s", raw)
	}
}

// TestSetSessionLabelEmptyOnAnUnlabelledSessionIsANoOp: removing a label
// that was never set must not touch the file at all — the desired state
// (nothing recorded) already holds.
func TestSetSessionLabelEmptyOnAnUnlabelledSessionIsANoOp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, ""); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a no-op removal must not change the file at all, before:\n%s\nafter:\n%s", before, after)
	}
}

func TestSetSessionLabelRefusesAMalformedSessionID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	cases := []string{
		"not-a-uuid",
		"",
		uuidA + "\nboard:\n  path: /etc/passwd",
		uuidA + ": injected",
	}
	for _, id := range cases {
		if err := SetSessionLabel(p, id, "label"); err == nil {
			t.Fatalf("a malformed sessionID %q must be refused", id)
		}
	}

	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("a refused write must leave the file untouched, before:\n%s\nafter:\n%s", before, after)
	}
}

func TestSetSessionLabelRefusesALabelContainingANewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionLabel(p, uuidA, "line one\nline two"); err == nil {
		t.Fatal("a label containing a newline must be refused")
	}
}

// TestSetSessionLabelRefusesANewlineEvenWhenItWouldParse guards against a
// weaker version of the newline check that only catches what yaml.v3's
// KnownFields decode itself rejects. yaml.Marshal always indents a
// multi-line block scalar's own content by exactly 4 spaces, regardless of
// the mapping's surrounding indentation — so on a file whose session_labels
// entries sit at 2 spaces (a shape Save itself never produces, but a person
// could easily hand-edit), the 4-space block content parses as valid,
// deeper-than-its-key YAML. Nothing downstream would ever catch that: it is
// this guard alone standing between an embedded newline and a label
// silently expanding into several physical lines.
func TestSetSessionLabelRefusesANewlineEvenWhenItWouldParse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "session_labels:\n" +
		"  " + uuidA + ": old\n" +
		"  " + uuidB + ": existing\n" +
		"server:\n  port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, "line one\nline two"); err == nil {
		t.Fatal("a label containing a newline must be refused even when the resulting YAML would still parse")
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Fatalf("a refused write must leave the file untouched, want:\n%s\ngot:\n%s", content, raw)
	}
}

// TestSetSessionLabelOnAFileWithNoSessionLabelsKeyIsAnError guards the same
// invariant SetField's own doc comment states: the section must already
// exist. A config missing it entirely (hand-edited down to something
// unusual, or from before this feature existed) must fail loudly rather
// than guess where to create the section.
func TestSetSessionLabelOnAFileWithNoSessionLabelsKeyIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionLabel(p, uuidA, "label"); err == nil {
		t.Fatal("a config with no session_labels key must be refused, not silently given one")
	}
}
