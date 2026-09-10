// internal/board/write_test.go
package board

import (
	"bytes"
	"errors"
	"os"
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

func TestSetFieldChangesOnlyTheTargetLine(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	// sample starts at progress 80; stage done requires progress 100 first.
	if err := SetField(p, "progress", "100"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(p)
	if err := SetField(p, "stage", "done"); err != nil {
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
	p := writeCard(t, t.TempDir(), "c.md", sample)
	if err := SetField(p, "session", "deadbeef"); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("only stage and progress are writable, got %v", err)
	}
}

func TestSetFieldRejectsInvalidValues(t *testing.T) {
	p := writeCard(t, t.TempDir(), "c.md", sample)
	if err := SetField(p, "stage", "almost"); err == nil {
		t.Fatal("an unknown stage must be refused")
	}
	if err := SetField(p, "progress", "55"); err == nil {
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

	if err := SetField(p, "progress", "100"); err != nil {
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
	if err := SetField(p, "progress", "0100"); err != nil {
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
	if err := SetField(p, "progress", "100"); err == nil {
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
	if err := SetField(p, "stage", "new"); err == nil {
		t.Fatal("a card without a frontmatter block must be refused")
	}
}

func TestSetFieldRefusesCardMissingTargetField(t *testing.T) {
	p := writeCard(t, t.TempDir(), "m.md", cardMissingProgress)
	if err := SetField(p, "progress", "20"); err == nil {
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
	if err := SetField(p, "stage", "done"); err == nil {
		t.Fatal("stage done must be refused while progress is not 100")
	}
	if err := SetField(p, "progress", "100"); err != nil {
		t.Fatalf("progress 100 must be allowed while stage is review: %v", err)
	}
	if err := SetField(p, "stage", "done"); err != nil {
		t.Fatalf("stage done must be allowed once progress is 100: %v", err)
	}
}

func TestSetFieldRefusesStartedStageWithoutSession(t *testing.T) {
	p := writeCard(t, t.TempDir(), "ns.md", cardNoSession)
	if err := SetField(p, "stage", "active"); err == nil {
		t.Fatal("a started stage must be refused while session is empty")
	}
}

func TestSetFieldRefusesProgressChangeAwayFrom100WhileDone(t *testing.T) {
	p := writeCard(t, t.TempDir(), "d.md", cardDone)
	if err := SetField(p, "progress", "80"); err == nil {
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
	if err := SetField(p, "progress", "100"); err != nil {
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
