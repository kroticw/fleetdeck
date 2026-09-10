package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestLocateRejectsGlobMetacharacters guards against an identifier that is
// itself a glob pattern reaching another session's transcript. Break it by
// removing the sessionUUIDPattern check in Locate and this test fails
// because Locate("*", root) returns the one transcript present instead of
// ErrNoTranscript.
func TestLocateRejectsGlobMetacharacters(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "-home-anon-example-project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(projectDir, "11111111-1111-4111-8111-111111111111.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Locate(root, "*"); !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("want ErrNoTranscript for a glob-metacharacter identifier, got %v", err)
	}
}

// TestLocateRejectsPathTraversal guards against an identifier that walks the
// glob outside projectsDir. Break it by removing the sessionUUIDPattern
// check in Locate and this test fails because Locate resolves the outside
// file instead of returning ErrNoTranscript.
func TestLocateRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	// projectsDir must be nested for the escape to land somewhere real: the
	// glob pattern is projectsDir/*/<id>.jsonl, so one ".." in the identifier
	// only cancels the "*" segment and stays inside projectsDir. A second
	// ".." is what walks out of projectsDir itself, exactly as the reviewer
	// demonstrated with Locate(root, "../../outside/secret").
	projects := filepath.Join(root, "projects")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(outside, "secret.jsonl")
	if err := os.WriteFile(secretFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Locate(projects, "../../outside/secret"); !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("want ErrNoTranscript for a path-traversal identifier, got %v", err)
	}
}

// TestLocatePicksMostRecentlyModifiedDuplicate guards the deliberate choice
// made when two project directories hold a transcript for the same UUID: the
// most recently modified one wins, not whichever directory name sorts first
// lexicographically. Break it by reverting to `matches[0]` on unsorted glob
// output and this test can return either file depending on glob order; with
// the fix it is always the one just touched.
func TestLocatePicksMostRecentlyModifiedDuplicate(t *testing.T) {
	root := t.TempDir()
	sessionUUID := "22222222-2222-4222-8222-222222222222"

	// Names are chosen so lexicographic order picks the stale one first —
	// "aaa" sorts before "zzz" — the exact opposite of the freshness order
	// this test requires, so a reversion to plain matches[0] fails loudly.
	oldDir := filepath.Join(root, "-home-anon-aaa-old-project")
	newDir := filepath.Join(root, "-home-anon-zzz-new-project")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(oldDir, sessionUUID+".jsonl")
	newFile := filepath.Join(newDir, sessionUUID+".jsonl")
	if err := os.WriteFile(oldFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldFile, older, older); err != nil {
		t.Fatal(err)
	}

	got, err := Locate(root, sessionUUID)
	if err != nil {
		t.Fatal(err)
	}
	if got != newFile {
		t.Fatalf("want the more recently modified transcript %q, got %q", newFile, got)
	}
}
