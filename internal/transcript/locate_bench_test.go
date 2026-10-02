package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Locate's cost is set by how many project directories the machine has
// accumulated, not by how many sessions are running: the glob pattern has a
// star in the middle, so every directory is read in full on every call. These
// benchmarks are what that sentence rests on — run them before changing how a
// transcript is found.
//
//	go test ./internal/transcript -run XXX -bench BenchmarkLocate -benchmem
//
// The panel calls Locate once per live session per collect cycle, so the
// per-call number here is multiplied by the fleet's size every couple of
// seconds. cmd/fleetdeck keeps the answer for a while (locatedTTL) for exactly
// that reason; BenchmarkLocateCacheHit is what a kept answer costs instead.
func BenchmarkLocate(b *testing.B) {
	for _, projects := range []int{1, 16, 64} {
		dir, id := transcriptTree(b, projects, 4)
		b.Run(fmt.Sprintf("projects_%d", projects), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Locate(dir, id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkLocateCacheHit is the other half of the comparison: what checking a
// path already known costs, which is one stat regardless of the tree's size.
func BenchmarkLocateCacheHit(b *testing.B) {
	dir, id := transcriptTree(b, 64, 4)
	path, err := Locate(dir, id)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := os.Stat(path); err != nil {
			b.Fatal(err)
		}
	}
}

// transcriptTree builds a projects directory of the shape Claude Code writes —
// <projects>/<project>/<uuid>.jsonl — with projects directories of perEach
// transcripts each, and returns it together with the id of one transcript in
// the last of them. The last, so a lookup walks the whole tree rather than
// stopping at the first name it reads.
func transcriptTree(tb testing.TB, projects, perEach int) (dir, sessionID string) {
	tb.Helper()
	dir = tb.TempDir()
	for p := range projects {
		project := filepath.Join(dir, fmt.Sprintf("-Users-someone-project-%d", p))
		if err := os.MkdirAll(project, 0o700); err != nil {
			tb.Fatal(err)
		}
		for s := range perEach {
			id := fmt.Sprintf("%08x-0000-4000-8000-%012x", p, s)
			line := `{"type":"assistant","timestamp":"2026-09-23T12:00:00Z","message":{"usage":{"input_tokens":1}}}` + "\n"
			if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(line), 0o600); err != nil {
				tb.Fatal(err)
			}
			sessionID = id
		}
	}
	return dir, sessionID
}
