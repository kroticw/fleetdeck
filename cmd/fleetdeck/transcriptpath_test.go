package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
)

// writeTranscriptIn lays out one project directory holding the session's
// transcript and returns the file's path. It is writeTranscript with the
// project name spelled out, so a test can build the two copies Locate has to
// choose between.
func writeTranscriptIn(t *testing.T, projectsDir, project string) string {
	t.Helper()
	dir := filepath.Join(projectsDir, project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sampleUUID+".jsonl")
	if err := os.WriteFile(path, []byte(assistantLine(sampleTokens)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTranscriptPathFindsTranscript(t *testing.T) {
	t.Parallel()
	projects := t.TempDir()
	want := writeTranscriptIn(t, projects, "one-project")

	got, err := NewCollector(config.Default(), nil, nil, projects).transcriptPath(sampleUUID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("transcriptPath = %q, want %q", got, want)
	}
}

// A session's transcript does not move while it runs, so the lookup is made
// once and served from memory afterwards — which is the whole point of the
// cache. A newer copy appearing under another project directory is what makes
// a cached answer observably different from a fresh Locate.
func TestTranscriptPathServesCachedPathWithinTTL(t *testing.T) {
	t.Parallel()
	projects := t.TempDir()
	first := writeTranscriptIn(t, projects, "a-project")

	c := NewCollector(config.Default(), nil, nil, projects)
	if _, err := c.transcriptPath(sampleUUID); err != nil {
		t.Fatal(err)
	}

	newer := writeTranscriptIn(t, projects, "b-project")
	if err := os.Chtimes(newer, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := c.transcriptPath(sampleUUID)
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("within the TTL the cached path must be served: got %q, want %q", got, first)
	}
}

// The cache is not permanent: a project directory renamed under a running
// panel leaves the old copy readable, so a lookup that never repeated would
// keep reporting a transcript nothing writes to any more.
func TestTranscriptPathRefreshesAfterTTL(t *testing.T) {
	t.Parallel()
	projects := t.TempDir()
	writeTranscriptIn(t, projects, "a-project")

	base := time.Now()
	c := NewCollector(config.Default(), nil, nil, projects)
	c.now = func() time.Time { return base }
	if _, err := c.transcriptPath(sampleUUID); err != nil {
		t.Fatal(err)
	}

	newer := writeTranscriptIn(t, projects, "b-project")
	if err := os.Chtimes(newer, base.Add(time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	c.now = func() time.Time { return base.Add(locatedTTL + time.Second) }

	got, err := c.transcriptPath(sampleUUID)
	if err != nil {
		t.Fatal(err)
	}
	if got != newer {
		t.Fatalf("after the TTL the newest transcript must win: got %q, want %q", got, newer)
	}
}

func TestTranscriptPathForgetsVanishedTranscript(t *testing.T) {
	t.Parallel()
	projects := t.TempDir()
	path := writeTranscriptIn(t, projects, "a-project")

	c := NewCollector(config.Default(), nil, nil, projects)
	if _, err := c.transcriptPath(sampleUUID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if got, err := c.transcriptPath(sampleUUID); err == nil {
		t.Fatalf("a transcript that is gone must not be served from the cache, got %q", got)
	}
}

// The path cache is pruned on the same rule as the context cache: a session no
// longer in the daemon's list leaves nothing behind, or the panel grows one
// entry per session it has ever seen.
func TestPruneForgetsPathsOfGoneSessions(t *testing.T) {
	t.Parallel()
	projects := t.TempDir()
	writeTranscriptIn(t, projects, "a-project")

	c := NewCollector(config.Default(), nil, nil, projects)
	if _, err := c.transcriptPath(sampleUUID); err != nil {
		t.Fatal(err)
	}
	c.pruneCaches(map[string]struct{}{})

	c.cacheMu.Lock()
	n := len(c.pathCache)
	c.cacheMu.Unlock()
	if n != 0 {
		t.Fatalf("path cache must be empty after the session is gone, has %d entries", n)
	}
}
