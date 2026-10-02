package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// saveAndReload writes c, reads the file back as text, and loads it again:
// what a round trip through this package does to a configuration, and the
// bytes it left behind.
func saveAndReload(t *testing.T, c Config) (Config, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil {
		t.Fatalf("what Save wrote does not load again: %v", err)
	}
	return back, string(raw)
}

const workersShape = `board:
    path: /Users/me/obsidian/board
workers:
    model: claude-fable-5
    permission_mode: acceptEdits
    sandbox: true
`

func TestTheWorkersSectionIsReadAndWrittenBack(t *testing.T) {
	c, err := loadText(t, workersShape)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Workers{Model: "claude-fable-5", PermissionMode: "acceptEdits", Sandbox: true}
	if c.Workers != want {
		t.Fatalf("Workers = %+v, want %+v", c.Workers, want)
	}
	back, _ := saveAndReload(t, c)
	if back.Workers != want {
		t.Fatalf("round trip gave %+v, want %+v", back.Workers, want)
	}
}

// Nothing said is the defaults, and the defaults are the launcher's to apply:
// a key written into the operator's file here would freeze today's default in
// it.
func TestAConfigWithoutWorkersWritesNone(t *testing.T) {
	c, err := loadText(t, "board:\n    path: /b\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Workers != (Workers{}) {
		t.Fatalf("Workers = %+v, want the zero value", c.Workers)
	}
	if _, raw := saveAndReload(t, c); strings.Contains(raw, "workers") {
		t.Fatalf("a configuration without workers was written with them:\n%s", raw)
	}
}

// A mode the CLI does not know would fail every start, one card at a time;
// the configuration is refused once, at load, with the list to pick from.
func TestAnUnknownPermissionModeIsRefused(t *testing.T) {
	_, err := loadText(t, "board:\n    path: /b\nworkers:\n    permission_mode: yolo\n")
	if err == nil || !strings.Contains(err.Error(), `workers.permission_mode "yolo"`) || !strings.Contains(err.Error(), "auto") {
		t.Fatalf("want a refusal naming the key and the choices, got %v", err)
	}
}
