package transcript

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDigestReturnsOldestFirstWithinLimit(t *testing.T) {
	steps, err := Digest("testdata/session.jsonl", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("fixture must yield steps; zero here means the parser had nothing to look at")
	}
	if len(steps) > 5 {
		t.Fatalf("limit ignored: got %d", len(steps))
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].At.Before(steps[i-1].At) {
			t.Fatal("steps must be ordered oldest first")
		}
	}
}

func TestDigestDropsMessagesWithoutText(t *testing.T) {
	steps, err := Digest("testdata/session.jsonl", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Text == "" {
			t.Fatal("a step without text is noise and must be dropped")
		}
	}
}

func TestDigestOnMissingFileIsTypedError(t *testing.T) {
	_, err := Digest(filepath.Join(t.TempDir(), "absent.jsonl"), 5)
	if !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("want ErrNoTranscript, got %v", err)
	}
}

func TestDigestOnEmptyFileIsNotSuccess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Digest(p, 5); err == nil {
		t.Fatal("an empty transcript must be reported, not silently pass as zero steps")
	}
}

func TestDigestSurvivesBrokenLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mixed.jsonl")
	good := `{"type":"assistant","timestamp":"2026-09-09T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`
	if err := os.WriteFile(p, []byte("{not json\n"+good+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Text != "ok" {
		t.Fatalf("one broken line must not discard the good ones: %+v", steps)
	}
}

// TestDigestDropsHousekeepingEnvelopes guards the noise filter ported from
// plugin/scripts/transcript_digest.py. The last five text-bearing lines are
// four housekeeping envelopes (local-command output, a command name/message
// pair, an interruption marker) and one real answer; only the real one must
// survive. Break it by removing the isNoise call in Digest's visit closure
// and this test fails with 5 steps instead of 1.
func TestDigestDropsHousekeepingEnvelopes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "housekeeping.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-09-09T00:00:01Z","message":{"role":"user","content":"<local-command-stdout>build ok</local-command-stdout>"}}`,
		`{"type":"user","timestamp":"2026-09-09T00:00:02Z","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","timestamp":"2026-09-09T00:00:03Z","message":{"role":"user","content":"<command-message>clear</command-message>"}}`,
		`{"type":"assistant","timestamp":"2026-09-09T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		`{"type":"assistant","timestamp":"2026-09-09T00:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"The deploy script now defaults DEPLOY_ENV to staging."}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("want only the real answer to survive, got %d steps: %+v", len(steps), steps)
	}
	if steps[0].Text != "The deploy script now defaults DEPLOY_ENV to staging." {
		t.Fatalf("want the real answer, got %q", steps[0].Text)
	}
}

// TestDigestNonPositiveLimitReturnsNoSteps pins down the behavior of a
// non-positive limit: zero steps, not "no limit" reading the whole file.
// Break it by reverting the `limit > 0 &&` removal in Digest and this test
// fails because the whole fixture's steps come back instead of none.
func TestDigestNonPositiveLimitReturnsNoSteps(t *testing.T) {
	for _, limit := range []int{0, -1, -5} {
		steps, err := Digest("testdata/session.jsonl", limit)
		if err != nil {
			t.Fatalf("limit %d: unexpected error: %v", limit, err)
		}
		if len(steps) != 0 {
			t.Fatalf("limit %d: want 0 steps, got %d", limit, len(steps))
		}
	}
}

// TestDigestDropsLineWithUnparsableTimestamp guards against a swallowed
// time.Parse error yielding a Step with a zero At field indistinguishable
// from a genuinely-timestamped line, which would silently corrupt the
// oldest-first ordering callers rely on. Break it by reverting to
// `at, _ := time.Parse(...)` and this test fails because the bad-timestamp
// line comes back with a zero time as though it were real.
func TestDigestDropsLineWithUnparsableTimestamp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad-timestamp.jsonl")
	bad := `{"type":"user","timestamp":"not-a-time","message":{"role":"user","content":"hello"}}`
	good := `{"type":"assistant","timestamp":"2026-09-09T00:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	if err := os.WriteFile(p, []byte(bad+"\n"+good+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("want the unparsable-timestamp line dropped, got %d steps: %+v", len(steps), steps)
	}
	if steps[0].Text != "hi" {
		t.Fatalf("want the good line to survive, got %q", steps[0].Text)
	}
}

// TestTextJoinsAdjacentBlocksWithSeparator guards against concatenating two
// text blocks with no separator, which runs the words at the seam together.
// Break it by reverting to `out += b.Text` in rawLine.text and this test
// fails on "onetwo" instead of "one\ntwo".
func TestTextJoinsAdjacentBlocksWithSeparator(t *testing.T) {
	r := rawLine{Message: struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}{Content: json.RawMessage(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`)}}
	if got, want := r.text(), "one\ntwo"; got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestDigestLimitKeepsMostRecent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "many.jsonl")
	lines := ""
	for i := 1; i <= 8; i++ {
		lines += `{"type":"user","timestamp":"2026-09-09T00:00:0` + string(rune('0'+i)) + `Z","message":{"role":"user","content":"msg` + string(rune('0'+i)) + `"}}` + "\n"
	}
	if err := os.WriteFile(p, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(steps))
	}
	if steps[len(steps)-1].Text != "msg8" {
		t.Fatalf("want the newest step last, got %q", steps[len(steps)-1].Text)
	}
	if steps[0].Text != "msg6" {
		t.Fatalf("want the limit to keep the most recent steps, got %q first", steps[0].Text)
	}
}
