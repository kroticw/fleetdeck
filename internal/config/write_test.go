package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetFieldPreservesCommentsAndIndentation is the operator's own
// acceptance test: a comment above the key, a trailing comment on the key's
// own line, and non-standard indentation must all survive a write that
// changes exactly one value. Save (full re-marshal from a Config struct)
// cannot make this promise at all -- it discards every byte of the original
// file -- which is exactly why SetField exists as a separate, narrower path.
func TestSetFieldPreservesCommentsAndIndentation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "orchestrator:\n" +
		"    # pinned session, set from the picker\n" +
		"    session: old-short-id  # do not touch by hand\n" +
		"server:\n" +
		"  port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetField(p, "orchestrator.session", "new-short-id"); err != nil {
		t.Fatalf("SetField: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)

	if !strings.Contains(got, "# pinned session, set from the picker") {
		t.Fatalf("the comment above the key must survive, got:\n%s", got)
	}
	if !strings.Contains(got, "# do not touch by hand") {
		t.Fatalf("the trailing comment on the key's own line must survive, got:\n%s", got)
	}
	if !strings.Contains(got, "    session: new-short-id") {
		t.Fatalf("the value must change while the original 4-space indentation survives, got:\n%s", got)
	}
	if strings.Contains(got, "old-short-id") {
		t.Fatalf("the old value must not remain anywhere in the file, got:\n%s", got)
	}
	if !strings.Contains(got, "port: 7777") {
		t.Fatalf("an untouched sibling section must survive byte for byte, got:\n%s", got)
	}

	// Exactly one value changed: re-parsing through the ordinary Load path
	// must see the new session and every other default untouched.
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("the written file must still parse: %v", err)
	}
	if cfg.OrchestratorSession != "new-short-id" {
		t.Fatalf("OrchestratorSession = %q, want new-short-id", cfg.OrchestratorSession)
	}
	if cfg.ServerPort != 7777 {
		t.Fatalf("an untouched field must keep its value, got ServerPort=%d", cfg.ServerPort)
	}
}

// TestSetFieldWritesAScalarThatWouldOtherwiseNeedQuoting covers the values
// SetField cannot just paste onto the line verbatim: an empty string (the
// picker's "unpin" case) and a string that looks like a different YAML type.
// yaml.v3 quotes each of these on its own marshal path, and SetField must
// match that or Load will read back something other than what was written.
func TestSetFieldWritesAScalarThatWouldOtherwiseNeedQuoting(t *testing.T) {
	for _, value := range []string{"", "true", "123"} {
		t.Run(value, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			cfg := Default()
			cfg.OrchestratorSession = "placeholder"
			if err := Save(p, cfg); err != nil {
				t.Fatal(err)
			}
			if err := SetField(p, "orchestrator.session", value); err != nil {
				t.Fatalf("SetField: %v", err)
			}
			got, err := Load(p)
			if err != nil {
				t.Fatalf("the written file must still parse: %v", err)
			}
			if got.OrchestratorSession != value {
				t.Fatalf("OrchestratorSession = %q, want %q", got.OrchestratorSession, value)
			}
		})
	}
}

// TestSetFieldDoesNotMatchASameNamedKeyOutsideTheTargetBlock guards the
// nesting-depth check itself: a top-level key that happens to share a name
// with the leaf being searched for, sitting right after the block that
// should contain it, must never be mistaken for it. Without the depth
// check, the search for "session" would keep scanning past the end of
// orchestrator's own block and land on this unrelated key instead.
func TestSetFieldDoesNotMatchASameNamedKeyOutsideTheTargetBlock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "orchestrator:\n" +
		"  other: value\n" +
		"session: decoy-at-top-level\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	err := SetField(p, "orchestrator.session", "changed")
	if err == nil {
		t.Fatal("orchestrator has no session key of its own; the top-level decoy must not be matched instead")
	}

	raw, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(raw), "session: decoy-at-top-level") {
		t.Fatalf("a refused write must not touch the decoy either, got:\n%s", string(raw))
	}
}

// TestSubstituteNestedFieldTopLevelKeyIgnoresASameNamedNestedKeyAboveIt
// guards the one depth check substituteNestedField still needs: nothing
// narrows the search window for the first key path element, so a same-named
// key nested inside an unrelated section — appearing earlier in the file —
// must not be mistaken for the real top-level "orchestrator:" block. Tested
// against substituteNestedField directly rather than through SetField: the
// decoy shape below has no counterpart in this package's own Config schema,
// so SetField's own after-the-fact parseability check would refuse it for
// an unrelated reason (an unknown field) before this property could be
// observed at all.
func TestSubstituteNestedFieldTopLevelKeyIgnoresASameNamedNestedKeyAboveIt(t *testing.T) {
	raw := "notify:\n" +
		"  orchestrator: not-the-real-one\n" +
		"orchestrator:\n" +
		"  session: real-value\n"

	out, err := substituteNestedField([]byte(raw), []string{"orchestrator", "session"}, "changed")
	if err != nil {
		t.Fatalf("substituteNestedField: %v", err)
	}

	got := string(out)
	if !strings.Contains(got, "orchestrator: not-the-real-one") {
		t.Fatalf("the nested decoy must survive untouched, got:\n%s", got)
	}
	if !strings.Contains(got, "  session: changed") {
		t.Fatalf("the real top-level orchestrator block must be the one that changed, got:\n%s", got)
	}
}

// TestSetFieldRefusesAValueContainingANewline guards the one-line-touched
// promise this function makes: yaml.v3 marshals a string with an embedded
// newline as a multi-line block scalar, which would splice extra lines into
// the file instead of rewriting the one line SetField was asked for.
func TestSetFieldRefusesAValueContainingANewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "orchestrator:\n  session: old\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetField(p, "orchestrator.session", "line one\nline two"); err == nil {
		t.Fatal("a value containing a newline must be refused, not turned into a multi-line block scalar")
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Fatalf("a refused write must leave the file untouched, got:\n%s", string(raw))
	}
}

// TestSetFieldDoesNotTreatAHashInsideAnUnquotedValueAsAComment pins YAML's
// own comment rule: a "#" only starts a comment when it is preceded by
// whitespace (or begins the value). `a#b` is the single scalar "a#b", not
// "a" followed by a comment "#b" -- treating it as the latter would splice a
// meaningless fragment of the discarded old value onto the new line.
func TestSetFieldDoesNotTreatAHashInsideAnUnquotedValueAsAComment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "orchestrator:\n  session: a#b\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetField(p, "orchestrator.session", "new"); err != nil {
		t.Fatalf("SetField: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if strings.Contains(got, "#b") {
		t.Fatalf("a hash with no preceding whitespace was part of the old value, not a comment, and must not reappear as one: %s", got)
	}
	if !strings.Contains(got, "session: new\n") {
		t.Fatalf("want a clean new value with nothing appended, got:\n%s", got)
	}
}

func TestSetFieldOnAFileWithNoSuchKeyIsAnError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := SetField(p, "orchestrator.session", "id")
	if err == nil {
		t.Fatal("a missing key must be refused, not silently inserted somewhere")
	}
}

func TestSetFieldOnAMissingFileIsAnError(t *testing.T) {
	err := SetField(filepath.Join(t.TempDir(), "nope.yaml"), "orchestrator.session", "id")
	if err == nil {
		t.Fatal("a config file that does not exist yet must be an error, not a silent no-op")
	}
}

// TestSetFieldRefusesAnEmptyKey guards the API contract itself, independent
// of any file content.
func TestSetFieldRefusesAnEmptyKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("server:\n  port: 7777\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetField(p, "", "x"); err == nil {
		t.Fatal("an empty key must be refused")
	}
}

// TestSetFieldChangesNothingElseInARealisticFile pins the promise at a
// coarser grain: diff the file before and after line by line, and demand
// that only the one targeted line differs.
func TestSetFieldChangesNothingElseInARealisticFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Default()
	cfg.BoardPath = "/home/op/board"
	cfg.OrchestratorSession = "abc123"
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	if err := SetField(p, "orchestrator.session", "def456"); err != nil {
		t.Fatalf("SetField: %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	beforeLines := strings.Split(string(before), "\n")
	afterLines := strings.Split(string(after), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("line count changed: %d before, %d after", len(beforeLines), len(afterLines))
	}
	changed := 0
	for i := range beforeLines {
		if beforeLines[i] != afterLines[i] {
			changed++
			if !strings.Contains(afterLines[i], "def456") {
				t.Fatalf("unexpected line changed at %d: %q -> %q", i, beforeLines[i], afterLines[i])
			}
		}
	}
	if changed != 1 {
		t.Fatalf("expected exactly one changed line, got %d", changed)
	}
}
