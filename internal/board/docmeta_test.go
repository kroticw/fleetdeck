package board

import (
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
	body := "---\n" + strings.Repeat("x: y\n", 2000) + "session: e62e1d58\n---\n"
	if got := DocSession(writeDoc(t, body)); got != "" {
		t.Fatalf("DocSession = %q past the head", got)
	}
}

func TestDocSessionOfAMissingFileIsEmpty(t *testing.T) {
	if got := DocSession(filepath.Join(t.TempDir(), "none.md")); got != "" {
		t.Fatalf("DocSession = %q", got)
	}
}
