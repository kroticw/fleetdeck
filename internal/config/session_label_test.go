package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// TestSetSessionLabelPreservesACommentOnABareHeaderWhenInsertingTheFirstEntry
// guards against a real bug caught in review: the first version of the
// insert path rewrote the "session_labels:" header line unconditionally
// whenever the section had no children yet, which silently dropped a
// trailing comment an operator had put on that very line. The header line
// is not one of the section's children, so none of the child-comment tests
// above ever exercised it.
func TestSetSessionLabelPreservesACommentOnABareHeaderWhenInsertingTheFirstEntry(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "session_labels:  # operator names go here\nserver:\n  port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, "orchestrator"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "session_labels:  # operator names go here") {
		t.Fatalf("the header's own comment must survive the first insert, got:\n%s", got)
	}
	if !strings.Contains(got, uuidA+": orchestrator") {
		t.Fatalf("the entry must actually be written, got:\n%s", got)
	}
}

// TestSetSessionLabelPreservesACommentOnAFlowEmptyHeaderWhenInsertingTheFirstEntry
// is the flow-style sibling of the test above: "session_labels: {}" is what
// Save itself writes, and this line does need rewriting (to open a block)
// unlike the bare case — but rewriting it must not drop a comment on the
// same line either.
func TestSetSessionLabelPreservesACommentOnAFlowEmptyHeaderWhenInsertingTheFirstEntry(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "session_labels: {}  # operator names go here\nserver:\n  port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, "orchestrator"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "session_labels:  # operator names go here") {
		t.Fatalf("the header's own comment must survive rewriting from flow to block style, got:\n%s", got)
	}
	if !strings.Contains(got, uuidA+": orchestrator") {
		t.Fatalf("the entry must actually be written, got:\n%s", got)
	}

	got2, err := Load(p)
	if err != nil {
		t.Fatalf("the written file must still parse: %v", err)
	}
	if got2.SessionLabels[uuidA] != "orchestrator" {
		t.Fatalf("SessionLabels[uuidA] = %q, want orchestrator", got2.SessionLabels[uuidA])
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

// TestConcurrentSetSessionLabelCallsDoNotLoseEachOthersWrite pins the fix
// for a real review finding: SetSessionLabel is a read-file, compute,
// atomic-rename cycle with no locking of its own, so two concurrent callers
// each read the same original bytes and each write back their own version
// — one silently overwriting the other's change. This route is reachable
// from two independent sessions' PATCH requests landing at the same time,
// which is exactly the shape this test drives. Without fileMu this test is
// flaky rather than reliably red, since Go's own file-write scheduling can
// happen to serialise two goroutines anyway — run with -count=10 or more
// while the fix is reverted to see it fail.
func TestConcurrentSetSessionLabelCallsDoNotLoseEachOthersWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("%08d-0000-0000-0000-000000000000", i)
			errs[i] = SetSessionLabel(p, id, fmt.Sprintf("label-%d", i))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("SetSessionLabel #%d: %v", i, err)
		}
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("the file must still parse after concurrent writes: %v", err)
	}
	if len(got.SessionLabels) != n {
		t.Fatalf("want all %d concurrent labels to survive, got %d: %+v", n, len(got.SessionLabels), got.SessionLabels)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%08d-0000-0000-0000-000000000000", i)
		want := fmt.Sprintf("label-%d", i)
		if got.SessionLabels[id] != want {
			t.Fatalf("SessionLabels[%s] = %q, want %q", id, got.SessionLabels[id], want)
		}
	}
}

// TestConcurrentSetFieldAndSetSessionLabelDoNotLoseEachOthersWrite is the
// cross-writer half of the same finding: the orchestrator pin (SetField)
// and a session label (SetSessionLabel) are two independently-triggerable
// routes writing the same file, and both must survive landing at once.
func TestConcurrentSetFieldAndSetSessionLabelDoNotLoseEachOthersWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var fieldErr, labelErr error
	go func() {
		defer wg.Done()
		fieldErr = SetField(p, "orchestrator.session", "pinned-session")
	}()
	go func() {
		defer wg.Done()
		labelErr = SetSessionLabel(p, uuidA, "orchestrator")
	}()
	wg.Wait()

	if fieldErr != nil {
		t.Fatalf("SetField: %v", fieldErr)
	}
	if labelErr != nil {
		t.Fatalf("SetSessionLabel: %v", labelErr)
	}

	got, err := Load(p)
	if err != nil {
		t.Fatalf("the file must still parse after concurrent writes: %v", err)
	}
	if got.OrchestratorSession != "pinned-session" {
		t.Fatalf("OrchestratorSession = %q, want pinned-session (lost to the concurrent label write)", got.OrchestratorSession)
	}
	if got.SessionLabels[uuidA] != "orchestrator" {
		t.Fatalf("SessionLabels[uuidA] = %q, want orchestrator (lost to the concurrent field write)", got.SessionLabels[uuidA])
	}
}

// TestSetSessionLabelCreatesTheSectionWhenTheKeyIsEntirelyMissing is a
// direct regression test for a real production failure: a config written by
// `fleetdeck init` before this feature existed has no session_labels key at
// all, and the first version of this function refused that with "has no
// session_labels key" — making the whole feature unusable for every
// operator who set up fleetdeck before the day this shipped, which is the
// ordinary case for this specific key (see SetSessionLabel's own comment
// for why session_labels is different from every other key this package
// writes). This is the first of the three initial shapes SetSessionLabel
// must handle — key missing entirely, key present with no children (covered
// above), key present with existing entries (also covered above) — and it
// is the one that broke in practice, caught by running against a real
// operator's real config rather than by any fixture, since every fixture up
// to this point went through Save, which always writes the key.
func TestSetSessionLabelCreatesTheSectionWhenTheKeyIsEntirelyMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "board:\n    path: \"\"\nserver:\n    port: 7777  # hand-tuned\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, "orchestrator"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.HasPrefix(got, content) {
		t.Fatalf("every byte that was already in the file must survive untouched, want prefix:\n%s\ngot:\n%s", content, got)
	}
	if !strings.Contains(got, "session_labels:") {
		t.Fatalf("a new session_labels section must be appended, got:\n%s", got)
	}
	if !strings.Contains(got, "# hand-tuned") {
		t.Fatalf("the existing comment must survive, got:\n%s", got)
	}

	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("the written file must still parse: %v", err)
	}
	if loaded.SessionLabels[uuidA] != "orchestrator" {
		t.Fatalf("SessionLabels[uuidA] = %q, want orchestrator", loaded.SessionLabels[uuidA])
	}
	if loaded.ServerPort != 7777 {
		t.Fatalf("an untouched sibling field must survive, got ServerPort=%d", loaded.ServerPort)
	}
}

// TestSetSessionLabelCreatesTheSectionEvenWithoutATrailingNewline guards the
// append path's own edge case: a hand-edited file with no trailing newline
// must not have its last existing line corrupted by the new section landing
// directly after it on the same line.
func TestSetSessionLabelCreatesTheSectionEvenWithoutATrailingNewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "server:\n    port: 7777" // deliberately no trailing newline
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, "orchestrator"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	loaded, err := Load(p)
	if err != nil {
		raw, _ := os.ReadFile(p)
		t.Fatalf("the written file must still parse: %v\ngot:\n%s", err, raw)
	}
	if loaded.SessionLabels[uuidA] != "orchestrator" {
		t.Fatalf("SessionLabels[uuidA] = %q, want orchestrator", loaded.SessionLabels[uuidA])
	}
	if loaded.ServerPort != 7777 {
		t.Fatalf("the line before the missing newline must not be corrupted, got ServerPort=%d", loaded.ServerPort)
	}
}

// TestSetSessionLabelEmptyOnAFileWithNoSessionLabelsKeyIsANoOp is removal's
// own version of TestSetSessionLabelCreatesTheSectionWhenTheKeyIsEntirelyMissing:
// removing a label from a section that is not even in the file yet is the
// desired state already holding, not an error and not a reason to create an
// empty section just to remove nothing from it.
func TestSetSessionLabelEmptyOnAFileWithNoSessionLabelsKeyIsANoOp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	content := "server:\n    port: 7777\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SetSessionLabel(p, uuidA, ""); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != content {
		t.Fatalf("a no-op removal must not change the file at all, before:\n%s\nafter:\n%s", content, raw)
	}
}
