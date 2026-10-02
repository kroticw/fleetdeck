package transcript

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestLastWordsAreTheNewestThingTheSessionSaid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path,
		assistantText("2026-09-24T10:00:00Z", "an older answer"),
		userPrompt("2026-09-24T10:01:00Z", "and now?"),
		assistantText("2026-09-24T10:02:00Z", "the branch is pushed"),
	)

	words, err := LastWords(path, 4000)
	if err != nil {
		t.Fatalf("LastWords: %v", err)
	}
	if words != "the branch is pushed" {
		t.Fatalf("the newest answer is what the session left behind, got %q", words)
	}
}

// A tool call is the session working, not the session speaking, and the words
// worth keeping are behind it.
func TestLastWordsLookPastToolCallsAndTheirResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path,
		assistantText("2026-09-24T10:00:00Z", "done, MR is open"),
		assistantToolUse("2026-09-24T10:01:00Z", "t1", "Bash"),
		toolResult("2026-09-24T10:02:00Z", "t1"),
	)

	words, err := LastWords(path, 4000)
	if err != nil {
		t.Fatalf("LastWords: %v", err)
	}
	if words != "done, MR is open" {
		t.Fatalf("a call and its result are not words, got %q", words)
	}
}

// The archive holds one row per session, so the words have to fit one. Cut on a
// rune boundary: the sessions here answer in Russian as often as in English.
func TestLastWordsAreCutToTheLimitOnARuneBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path, assistantText("2026-09-24T10:00:00Z", strings.Repeat("я", 50)))

	words, err := LastWords(path, 10)
	if err != nil {
		t.Fatalf("LastWords: %v", err)
	}
	if []rune(words)[0] != 'я' || len([]rune(words)) > 11 {
		t.Fatalf("the cut must keep whole runes and stay near the limit, got %q", words)
	}
	if !strings.HasSuffix(words, "…") {
		t.Fatalf("a cut answer must say it was cut: %q", words)
	}
}

// Nothing said is not an empty digest: an archive entry with no words in it is
// the loss the archive exists to prevent, so the caller has to see this fail.
func TestLastWordsRefuseATranscriptTheSessionNeverSpokeIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path, userPrompt("2026-09-24T10:00:00Z", "take the card"))

	if _, err := LastWords(path, 4000); !errors.Is(err, ErrNoWords) {
		t.Fatalf("a transcript with nothing said must be ErrNoWords, got %v", err)
	}
}

func TestLastWordsReportAMissingTranscript(t *testing.T) {
	_, err := LastWords(filepath.Join(t.TempDir(), "gone.jsonl"), 4000)
	if !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("a transcript that is not there must be ErrNoTranscript, got %v", err)
	}
}

// Thinking is not an answer: it is the model's own working, and it is not what
// the session told the operator.
func TestLastWordsSkipThinkingWithNoTextBesideIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path,
		assistantText("2026-09-24T10:00:00Z", "the fix is in"),
		assistantThinking("2026-09-24T10:01:00Z"),
	)

	words, err := LastWords(path, 4000)
	if err != nil {
		t.Fatalf("LastWords: %v", err)
	}
	if words != "the fix is in" {
		t.Fatalf("thinking is not speech, got %q", words)
	}
}
