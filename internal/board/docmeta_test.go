package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoc(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestDocSessionReadsTheShortIDFromTheFrontmatter(t *testing.T) {
	path := writeDoc(t, "---\ndate: 2026-09-27\nsession: e62e1d58\ncards: [T-090]\n---\n# Doc\n")
	if got := DocSession(path); got != "e62e1d58" {
		t.Fatalf("DocSession = %q, want e62e1d58", got)
	}
}

func TestDocSessionIsEmptyWithoutAFrontmatter(t *testing.T) {
	if got := DocSession(writeDoc(t, "# Doc\n\nsession: e62e1d58\n")); got != "" {
		t.Fatalf("a session line in the body is not the author: %q", got)
	}
}

func TestDocSessionRefusesAValueThatIsNotAShortID(t *testing.T) {
	for _, value := range []string{"orchestrator", "e62e1", "e62e1d58e62e1d58", `""`, "[a, b]"} {
		if got := DocSession(writeDoc(t, "---\nsession: "+value+"\n---\n")); got != "" {
			t.Errorf("session %s: DocSession = %q, want empty", value, got)
		}
	}
}

func TestDocSessionIgnoresAFrontmatterPastTheHead(t *testing.T) {
	// Only the head is read: a document is listed on every card the panel
	// opens, and a frontmatter that runs this far is not one.
	// Distinct keys: a YAML map with one key repeated does not parse at all,
	// which would make this pass whatever the head's size.
	var b strings.Builder
	b.WriteString("---\n")
	for i := range 2000 {
		fmt.Fprintf(&b, "k%d: v\n", i)
	}
	b.WriteString("session: e62e1d58\n---\n")
	if b.Len() <= docHead {
		t.Fatalf("the frontmatter is %d bytes, not past the %d-byte head", b.Len(), docHead)
	}
	if got := DocSession(writeDoc(t, b.String())); got != "" {
		t.Fatalf("DocSession = %q past the head", got)
	}
	// The same frontmatter within the head is read: the limit, not the
	// frontmatter's shape, is what drops it above.
	short := "---\nk0: v\nsession: e62e1d58\n---\n"
	if got := DocSession(writeDoc(t, short)); got != "e62e1d58" {
		t.Fatalf("DocSession = %q within the head", got)
	}
}

func TestDocSessionOfAMissingFileIsEmpty(t *testing.T) {
	if got := DocSession(filepath.Join(t.TempDir(), "none.md")); got != "" {
		t.Fatalf("DocSession = %q", got)
	}
}
