package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const commentedConfig = `# the operator's own notes
board:
    path: /b # where the cards are
server:
    port: 7777
workers:
    model: opus # keep
`

func writeConfigText(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The settings page writes keys that may not be in the file yet, and must
// leave everything it did not touch -- comments included -- as it was.
func TestSetValuesWritesNestedKeysAndKeepsTheRest(t *testing.T) {
	p := writeConfigText(t, commentedConfig)
	err := SetValues(p, map[string]any{
		"workers.effort":          "xhigh",
		"executors.codex.enabled": true,
		"executors.codex.model":   "gpt-6.1-sol",
		"ui.terminal_font_size":   14,
		"notify.enabled.waiting":  false,
		"workers.model":           "",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	text := string(raw)
	for _, kept := range []string{"# the operator's own notes", "# where the cards are"} {
		if !strings.Contains(text, kept) {
			t.Fatalf("comment %q lost:\n%s", kept, text)
		}
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("written file does not load: %v\n%s", err, text)
	}
	if c.Workers.Effort != "xhigh" || c.Workers.Model != "" || !c.Codex.Enabled || c.Codex.Model != "gpt-6.1-sol" ||
		c.UI.TerminalFontSize != 14 || c.Notify.Waiting || !c.Notify.Failed || c.ServerPort != 7777 || c.BoardPath != "/b" {
		t.Fatalf("config = %+v\n%s", c, text)
	}
	if strings.Contains(text, "model: opus") {
		t.Fatalf("an emptied key stayed:\n%s", text)
	}
}

// A value the configuration refuses never reaches the file.
func TestSetValuesRefusesAnInvalidValueAndLeavesTheFile(t *testing.T) {
	p := writeConfigText(t, commentedConfig)
	if err := SetValues(p, map[string]any{"workers.effort": "huge"}); err == nil {
		t.Fatal("an unknown effort was written")
	}
	if err := SetValues(p, map[string]any{"nosuch.key": "x"}); err == nil {
		t.Fatal("an unknown key was written")
	}
	if raw, _ := os.ReadFile(p); string(raw) != commentedConfig {
		t.Fatalf("the file changed:\n%s", raw)
	}
}
