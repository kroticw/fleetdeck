package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocateFindsTranscriptUnderProjectDir(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "-home-anon-example-project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionUUID := "00000000-0000-4000-8000-000000000000"
	want := filepath.Join(projectDir, sessionUUID+".jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Locate(root, sessionUUID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestLocateOnUnknownSessionIsTypedError(t *testing.T) {
	root := t.TempDir()
	if _, err := Locate(root, "no-such-session"); !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("want ErrNoTranscript, got %v", err)
	}
}
