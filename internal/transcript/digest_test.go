package transcript

import (
	"errors"
	"os"
	"path/filepath"
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
