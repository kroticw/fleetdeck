package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSaveOutputOfASingleFleetConfigIsPinned pins, byte for byte, what Save
// writes for a configuration that uses none of the keys added after it was
// shaped. The golden text was captured from Save before the fleets and name
// keys existed: a key added later must not appear in, reorder, or reformat
// the file of an operator who never uses it.
func TestSaveOutputOfASingleFleetConfigIsPinned(t *testing.T) {
	c := Default()
	c.BoardPath = "/Users/me/obsidian/board"
	c.DocsPaths = []string{"/Users/me/obsidian/board/docs"}
	c.OrchestratorSession = "06a1f607"
	c.SessionLabels = map[string]string{"06a1f607-1a29-4fb4-a02c-f1e7c5cf59a8": "оркестр"}
	c.Notify.SilenceAfter = 30 * time.Minute

	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != goldenSingleFleetSave {
		t.Fatalf("Save output changed:\n--- got ---\n%s\n--- want ---\n%s", got, goldenSingleFleetSave)
	}
}

const goldenSingleFleetSave = `board:
    path: /Users/me/obsidian/board
docs:
    paths:
        - /Users/me/obsidian/board/docs
orchestrator:
    session: 06a1f607
session_labels:
    06a1f607-1a29-4fb4-a02c-f1e7c5cf59a8: оркестр
notify:
    enabled:
        waiting: true
        failed: true
        silent: true
        card_blocked: true
    silence_after: 30m0s
daemon:
    poll_interval: 2s
usage:
    enabled: true
server:
    port: 7777
statusline:
    wrap: ""
    rate_limits_path: ""
`
