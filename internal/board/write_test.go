// internal/board/write_test.go
package board

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

const cardMissingProgress = `---
zone: planned
stage: review
session: abc12345
repo: work/thing
created: 2026-09-09
---

body
`

const cardNoSession = `---
zone: planned
stage: new
progress: 0
session: ""
repo: work/thing
created: 2026-09-09
---

body
`

const cardDone = `---
zone: planned
stage: done
progress: 100
session: abc12345
repo: work/thing
created: 2026-09-09
---

body
`

const cardDuplicateKey = `---
zone: planned
stage: review
progress: 80
progress: 90
session: abc12345
repo: work/thing
created: 2026-09-09
---

body
`

// The precondition exists for the drag: a card is moved by a hand working
// from a snapshot up to a second old, and the agent that owns the card may
// have written the field in between.
func TestSetFieldRefusesWhenTheCardNoLongerHoldsTheExpectedValue(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample) // stage review
	was := "new"
	before, _ := os.ReadFile(p)
	err := SetField(p, "stage", "blocked", &was)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale, got %v", err)
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) {
		t.Fatal("a refused write changed the card")
	}
}

func TestSetFieldWritesWhenTheExpectedValueIsTheOneOnDisk(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample) // stage review
	was := "review"
	if err := SetField(p, "stage", "blocked", &was); err != nil {
		t.Fatal(err)
	}
	c, _ := ParseCard(p)
	if c.Stage != "blocked" {
		t.Fatalf("stage reads back as %q", c.Stage)
	}
}

// progress arrives as a number from a snapshot and as a string here, and the
// two have to compare equal: "080" and 80 are the same progress.
func TestSetFieldComparesTheExpectedProgressAsANumber(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample) // progress 80
	was := "080"
	if err := SetField(p, "progress", "100", &was); err != nil {
		t.Fatal(err)
	}
}

// An empty session is a value like any other, so the precondition has to be
// able to name it — which is why it is a pointer and not an empty string.
func TestSetFieldTakesAnEmptySessionAsTheExpectedValue(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", cardNoSession)
	was := ""
	if err := SetField(p, "session", "abc12345", &was); err != nil {
		t.Fatal(err)
	}
	again := ""
	if err := SetField(p, "session", "deadbeef", &again); !errors.Is(err, ErrStale) {
		t.Fatalf("the card holds a session now, want ErrStale, got %v", err)
	}
}

func TestSetFieldChangesOnlyTheTargetLine(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	// sample starts at progress 80; stage done requires progress 100 first.
	if err := SetField(p, "progress", "100", nil); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	if err := SetField(p, "stage", "done", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	bl, al := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	if len(bl) != len(al) {
		t.Fatalf("line count changed: %d -> %d", len(bl), len(al))
	}
	diff := 0
	for i := range bl {
		if bl[i] != al[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Fatalf("exactly one line must change, %d changed", diff)
	}
	if !strings.Contains(string(after), "stage: done") {
		t.Fatal("stage was not written")
	}
}

func TestSetFieldRefusesFieldsThePanelDoesNotOwn(t *testing.T) {
	for _, field := range []string{"id", "zone", "repo", "created", "title"} {
		t.Run(field, func(t *testing.T) {
			p := writeCard(t, t.TempDir(), "c.md", sample)
			if err := SetField(p, field, "whatever", nil); !errors.Is(err, ErrUnknownField) {
				t.Fatalf("%s is not the panel's to write, got %v", field, err)
			}
		})
	}
}

func TestSetFieldRejectsInvalidValues(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	if err := SetField(p, "stage", "almost", nil); err == nil {
		t.Fatal("an unknown stage must be refused")
	}
	if err := SetField(p, "progress", "55", nil); err == nil {
		t.Fatal("progress outside the allowed ladder must be refused")
	}
}

// TestSetFieldKeepsBodyContentItDidNotWrite checks that the panel's write does
// not clobber body content that was already on disk when it re-read the file.
// It does NOT exercise a concurrent writer: the append below happens and
// completes before SetField is ever called, so this test cannot observe (and
// says nothing about) the loss of a write that lands between SetField's
// re-read and its rename.
func TestSetFieldKeepsBodyContentItDidNotWrite(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("- новая строка лога от агента\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if err := SetField(p, "progress", "100", nil); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), "новая строка лога от агента") {
		t.Fatal("body content already on disk before the write must survive it")
	}
	if !strings.Contains(string(after), "progress: 100") {
		t.Fatal("progress was not written")
	}
}

// TestSetFieldWritesNormalizedProgressNotRawString guards against the
// caller's raw string reaching disk: "0100" passes strconv.Atoi as 100, but
// written verbatim it is a leading-zero YAML scalar that yaml.v3 resolves as
// octal on the next read.
func TestSetFieldWritesNormalizedProgressNotRawString(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	if err := SetField(p, "progress", "0100", nil); err != nil {
		t.Fatal(err)
	}
	c, err := ParseCard(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Progress != 100 {
		t.Fatalf("progress must be normalized to 100, got %d", c.Progress)
	}
}

func TestSetFieldRefusesCardWithDuplicateFrontmatterKey(t *testing.T) {
	p := writeCard(t, t.TempDir(), "dup.md", cardDuplicateKey)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetField(p, "progress", "100", nil); err == nil {
		t.Fatal("a card whose frontmatter does not unmarshal must be refused")
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused write must not touch the file")
	}
}

func TestSetFieldRefusesCardWithoutFrontmatter(t *testing.T) {
	p := writeCard(t, t.TempDir(), "n.md", "# no frontmatter\n")
	if err := SetField(p, "stage", "new", nil); err == nil {
		t.Fatal("a card without a frontmatter block must be refused")
	}
}

func TestSetFieldRefusesCardMissingTargetField(t *testing.T) {
	p := writeCard(t, t.TempDir(), "m.md", cardMissingProgress)
	if err := SetField(p, "progress", "20", nil); err == nil {
		t.Fatal("a card without a progress field must be refused")
	}
}

// TestSubstituteFieldRefusesLineCountChange exercises the line-count guard
// directly with a value containing a newline. SetField's own validation
// never lets such a value through for stage or progress, so the guard is
// otherwise unreachable from a test.
func TestSubstituteFieldRefusesLineCountChange(t *testing.T) {
	if _, err := substituteField([]byte(sample), "stage", "active\nextra"); err == nil {
		t.Fatal("a substitution that adds a line must be refused")
	}
}

func TestSetFieldEnforcesDoneRequiresProgress100(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample) // progress 80, stage review, session set
	if err := SetField(p, "stage", "done", nil); err == nil {
		t.Fatal("stage done must be refused while progress is not 100")
	}
	if err := SetField(p, "progress", "100", nil); err != nil {
		t.Fatalf("progress 100 must be allowed while stage is review: %v", err)
	}
	if err := SetField(p, "stage", "done", nil); err != nil {
		t.Fatalf("stage done must be allowed once progress is 100: %v", err)
	}
}

func TestSetFieldRefusesStartedStageWithoutSession(t *testing.T) {
	p := writeCard(t, t.TempDir(), "ns.md", cardNoSession)
	if err := SetField(p, "stage", "active", nil); err == nil {
		t.Fatal("a started stage must be refused while session is empty")
	}
}

// A cross-field refusal reaches the operator through the page, which can only
// translate it by a code: the wording changes, the code does not. Every
// started stage is refused by the one rule, so all four carry the one code.
func TestCrossFieldRefusalsCarryACode(t *testing.T) {
	cases := map[string]struct {
		card  string
		field string
		value string
		code  string
	}{
		"active without session":   {cardNoSession, "stage", "active", "session_required"},
		"review without session":   {cardNoSession, "stage", "review", "session_required"},
		"done without session":     {cardNoSession, "stage", "done", "session_required"},
		"blocked without session":  {cardNoSession, "stage", "blocked", "session_required"},
		"done before progress 100": {sample, "stage", "done", "done_needs_progress_100"},
		"progress off 100 at done": {cardDone, "progress", "80", "done_holds_progress_100"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeCard(t, t.TempDir(), "c.md", tc.card)
			err := SetField(p, tc.field, tc.value, nil)
			var rule *RuleRefusal
			if !errors.As(err, &rule) {
				t.Fatalf("want a RuleRefusal, got %v", err)
			}
			if rule.Code != tc.code {
				t.Fatalf("want code %q, got %q", tc.code, rule.Code)
			}
			if rule.Error() == "" {
				t.Fatal("a refusal must still say why in words: the page falls back to them for a code it cannot translate")
			}
		})
	}
}

// The code becomes part of a dictionary key in web/js/i18n.js
// (card_refused_<code>).
func TestRuleRefusalCodesCanBeDictionaryKeys(t *testing.T) {
	ok := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for _, code := range ruleCodes {
		if !ok.MatchString(code) {
			t.Errorf("rule code %q is no key: lower-case words joined by underscores", code)
		}
	}
}

func TestSetFieldRefusesProgressChangeAwayFrom100WhileDone(t *testing.T) {
	p := writeCard(t, t.TempDir(), "d.md", cardDone)
	if err := SetField(p, "progress", "80", nil); err == nil {
		t.Fatal("progress must be refused away from 100 while stage is done")
	}
}

// TestSetFieldPreservesFileMode guards FIX 3's mode preservation: the
// atomic-write path must Chmod the temp file to the original's mode before
// the rename, not silently normalize every card to whatever the temp file
// happened to be created with.
func TestSetFieldPreservesFileMode(t *testing.T) {
	dir := t.TempDir()
	p := writeCard(t, dir, "c.md", sample)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetField(p, "progress", "100", nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("file mode must be preserved across an atomic write, got %o", info.Mode().Perm())
	}
}

// A card the panel itself created carries no session line at all
// (CreateCard's template wrote five fields), so writing a short id into one
// has to add the line rather than substitute it.
const cardWithoutSessionLine = `---
id: T-009
zone: planned
stage: new
progress: 0
created: 2026-09-09
---

body
`

func TestSetFieldWritesASessionShortID(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", cardNoSession)
	if err := SetField(p, "session", "abc12345", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "session: abc12345") {
		t.Fatalf("session was not written:\n%s", raw)
	}
}

func TestSetFieldRefusesASessionThatIsNoShortID(t *testing.T) {
	for _, value := range []string{"", "zz", "нехекс", "abcdef0123456", "abc 123"} {
		t.Run(value, func(t *testing.T) {
			p := writeCard(t, t.TempDir(), "c.md", cardNoSession)
			before, _ := os.ReadFile(p)
			if err := SetField(p, "session", value, nil); err == nil {
				t.Fatalf("session %q was accepted", value)
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Fatal("a refused write changed the card")
			}
		})
	}
}

// The line is added in the frontmatter and nowhere else: a card body may
// contain a line of its own that looks like a field.
func TestSetFieldAddsASessionLineToACardThatHasNone(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", cardWithoutSessionLine)
	if err := SetField(p, "session", "abc12345", nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	lines := strings.Split(string(raw), "\n")
	if got, want := len(lines), len(strings.Split(cardWithoutSessionLine, "\n"))+1; got != want {
		t.Fatalf("line count %d, want %d:\n%s", got, want, raw)
	}
	c, err := ParseCard(p)
	if err != nil {
		t.Fatalf("the card no longer parses: %v", err)
	}
	if c.Session != "abc12345" {
		t.Fatalf("session reads back as %q", c.Session)
	}
}

// The order the panel writes in: the short id first, the stage second. The
// board refuses a started stage while session is empty, so the two writes
// cannot be swapped and the first one has to be enough on its own.
func TestASessionWrittenFirstLetsTheStageBeSetToActive(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", cardWithoutSessionLine)
	if err := SetField(p, "stage", "active", nil); err == nil {
		t.Fatal("stage active was accepted on a card with no session")
	}
	if err := SetField(p, "session", "abc12345", nil); err != nil {
		t.Fatal(err)
	}
	if err := SetField(p, "stage", "active", nil); err != nil {
		t.Fatalf("stage active after a session was written: %v", err)
	}
}
