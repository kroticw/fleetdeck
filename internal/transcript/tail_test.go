package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestReverseLinesWithTinyChunksReconstructsExactOrder forces a tiny first
// chunk so nearly every read lands mid-line, then checks the full recovered
// sequence against the exact reverse of what was written. This is the test
// that actually exercises the "drop the possibly-partial first line of a
// non-initial chunk" guard in reverseLines: with a normal 64KB chunk the small
// fixtures elsewhere in this package never cut a line in half, so a broken
// guard would slip through unnoticed there.
func TestReverseLinesWithTinyChunksReconstructsExactOrder(t *testing.T) {
	old := startChunk
	startChunk = 8
	t.Cleanup(func() { startChunk = old })

	var want []string
	var b strings.Builder
	for i := 0; i < 50; i++ {
		line := fmt.Sprintf("item-%02d-%s", i, strings.Repeat("z", i%7))
		want = append(want, line)
		b.WriteString(line + "\n")
	}
	p := filepath.Join(t.TempDir(), "tiny-chunks.txt")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got []string
	if err := reverseLines(f, func(line []byte) bool {
		got = append(got, string(line))
		return false
	}); err != nil {
		t.Fatal(err)
	}

	wantReversed := make([]string, len(want))
	for i, s := range want {
		wantReversed[len(want)-1-i] = s
	}
	if !reflect.DeepEqual(got, wantReversed) {
		t.Fatalf("tail read with a tiny chunk corrupted line boundaries:\n got  %q\n want %q", got, wantReversed)
	}
}

// TestReverseLinesCrossesChunkBoundaries builds a file much larger than the
// initial tail-read chunk so that reverseLines must double its read at least
// once to reach lines near the beginning of the file. It exists because every
// other test in this package fits in a single chunk and would stay green even
// if the chunk-growth loop in tail.go were deleted outright.
func TestReverseLinesCrossesChunkBoundaries(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.jsonl")

	var b strings.Builder
	const total = 4000 // long enough to clear startChunk (64KB) several times over
	for i := 0; i < total; i++ {
		fmt.Fprintf(&b, "line %04d %s\n", i, strings.Repeat("x", 40))
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if int64(b.Len()) <= startChunk {
		t.Fatalf("fixture too small to exercise chunk growth: %d bytes", b.Len())
	}

	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var seen []string
	err = reverseLines(f, func(line []byte) bool {
		seen = append(seen, string(line))
		return len(seen) >= total // walk the whole file, newest to oldest
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != total {
		t.Fatalf("want %d lines visited, got %d", total, len(seen))
	}
	// seen is newest-first; the very first line visited must be the last line
	// written, and the very last line visited must be the first line written.
	if want := fmt.Sprintf("line %04d ", total-1); !strings.HasPrefix(seen[0], want) {
		t.Fatalf("newest line wrong: got %q", seen[0])
	}
	if want := "line 0000 "; !strings.HasPrefix(seen[len(seen)-1], want) {
		t.Fatalf("oldest line wrong: got %q", seen[len(seen)-1])
	}
	// Spot-check a line that sits well before the first chunk boundary to make
	// sure nothing was skipped or duplicated while growing the read.
	mid := total / 2
	found := false
	for _, line := range seen {
		if strings.HasPrefix(line, fmt.Sprintf("line %04d ", mid)) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("line %d was never visited", mid)
	}
}

// TestDigestOverLargeFile exercises Digest itself, not just reverseLines,
// against a file far bigger than one tail chunk, mixing broken and textless
// lines throughout so the limit is only satisfied after crossing a chunk
// boundary.
func TestDigestOverLargeFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.jsonl")

	var b strings.Builder
	const total = 3000
	for i := 0; i < total; i++ {
		switch i % 3 {
		case 0:
			fmt.Fprintf(&b, `{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"step %d"}}`+"\n", i)
		case 1:
			b.WriteString("{not json for line " + fmt.Sprint(i) + "\n")
		default:
			fmt.Fprintf(&b, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"noop"}]}}`+"\n")
		}
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if int64(b.Len()) <= startChunk {
		t.Fatalf("fixture too small to exercise chunk growth: %d bytes", b.Len())
	}

	steps, err := Digest(p, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 {
		t.Fatalf("want 4 steps, got %d", len(steps))
	}
	last := total - 1 // total-1 is index 2999, which is (i%3==2), textless; the
	// nearest matching (i%3==0) step below it is what Digest must surface last.
	for last%3 != 0 {
		last--
	}
	if want := fmt.Sprintf("step %d", last); steps[len(steps)-1].Text != want {
		t.Fatalf("want newest step %q last, got %q", want, steps[len(steps)-1].Text)
	}
}
