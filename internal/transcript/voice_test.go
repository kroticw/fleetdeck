package transcript

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Transcript lines for the voice tests, shaped like Claude Code 2.1.269 writes them
// but cut down to the fields ReadVoice looks at.

func assistantToolUse(at, id, tool string) string {
	return `{"type":"assistant","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"` + tool + `","input":{}}]}}`
}

func assistantText(at, text string) string {
	return `{"type":"assistant","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`
}

func assistantThinking(at string) string {
	return `{"type":"assistant","timestamp":"` + at + `","message":{"role":"assistant","content":[{"type":"thinking","thinking":""}]}}`
}

func toolResult(at, id string) string {
	return `{"type":"user","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}`
}

func userPrompt(at, text string) string {
	return `{"type":"user","timestamp":"` + at + `","message":{"role":"user","content":"` + text + `"}}`
}

func queuedMessage(at string) string {
	return `{"type":"queue-operation","operation":"enqueue","timestamp":"` + at + `","content":"<agent-message from=\"06a1f607\">are you there?</agent-message>"}`
}

func reminder(at string) string {
	return `{"type":"attachment","timestamp":"` + at + `","attachment":{"type":"total_tokens_reminder"}}`
}

func untimed(kind string) string {
	return `{"type":"` + kind + `"}`
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestVoiceIgnoresWhatOthersWriteIntoTheTranscript is T-047 as a test. An incoming
// message lands in the transcript the moment it is sent, whether or not the session
// can read it, and so does every runtime attachment and bookkeeping line. None of that
// is the session saying anything. Measured on a live frozen session on 2026-09-12: a
// message sent to it moved the file's mtime from 82 seconds old to 5, while the last
// line the session wrote itself stayed where it was.
func TestVoiceIgnoresWhatOthersWriteIntoTheTranscript(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		userPrompt("2026-09-10T19:40:00Z", "go"),
		assistantToolUse("2026-09-10T19:47:28Z", "t1", "Bash"),
		toolResult("2026-09-10T19:47:47Z", "t1"),
		reminder("2026-09-10T19:47:47Z"),
		assistantToolUse("2026-09-10T19:48:08Z", "t2", "mcp__claude-agents__send_message"),
		queuedMessage("2026-09-10T19:53:49Z"),
		untimed("last-prompt"),
		untimed("cost-state"),
	)
	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := at(t, "2026-09-10T19:48:08Z"); !v.LastSpoke.Equal(want) {
		t.Fatalf("the last thing the session said was opening the call at %s; got %s", want, v.LastSpoke)
	}
}

func TestVoiceReportsTheCallTheSessionIsStandingIn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		assistantThinking("2026-09-12T18:00:00Z"),
		assistantToolUse("2026-09-12T18:00:01Z", "r1", "Read"),
		queuedMessage("2026-09-12T18:05:00Z"),
	)
	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.InCall == nil {
		t.Fatal("an unanswered tool_use at the tail is a call the session is standing in")
	}
	if v.InCall.Tool != "Read" || !v.InCall.Since.Equal(at(t, "2026-09-12T18:00:01Z")) {
		t.Fatalf("want Read since 18:00:01, got %+v", *v.InCall)
	}
}

func TestVoiceAnsweredCallsAreNotStoodIn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		assistantToolUse("2026-09-12T18:00:00Z", "a", "Bash"),
		toolResult("2026-09-12T18:00:02Z", "a"),
		reminder("2026-09-12T18:00:02Z"),
	)
	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.InCall != nil {
		t.Fatalf("every call has its result, so the session is on the model's side, not in a call: %+v", *v.InCall)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:00:02Z")) {
		t.Fatalf("the result is the last thing said, got %s", v.LastSpoke)
	}
}

// Parallel calls are written as one tool_use line each, and their results come back
// one by one. The call still open is the one with no result, wherever it sits in the
// batch.
func TestVoiceFindsTheOpenCallInAParallelBatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		toolResult("2026-09-12T17:59:00Z", "earlier"),
		assistantToolUse("2026-09-12T18:00:00Z", "a", "Read"),
		assistantToolUse("2026-09-12T18:00:00Z", "b", "Bash"),
		assistantToolUse("2026-09-12T18:00:01Z", "c", "Grep"),
		toolResult("2026-09-12T18:00:02Z", "c"),
		toolResult("2026-09-12T18:00:03Z", "b"),
	)
	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.InCall == nil || v.InCall.Tool != "Read" {
		t.Fatalf("Read is the one call in the batch with no result, got %+v", v.InCall)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:00:03Z")) {
		t.Fatalf("a sibling's result arriving is the session hearing back, got %s", v.LastSpoke)
	}
}

// The shape of 5bc37caf after it was resumed: the call it froze in never got a result,
// because the process holding it died, and the session carried on after the resume.
// That call is history, not something it is standing in now.
func TestVoiceACallAbandonedBeforeLaterSpeechIsNotOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		assistantToolUse("2026-09-10T19:48:08Z", "s", "mcp__claude-agents__send_message"),
		queuedMessage("2026-09-10T19:53:49Z"),
		userPrompt("2026-09-11T04:37:37Z", "Continue from where you left off."),
		assistantText("2026-09-11T04:37:37Z", "No response requested."),
	)
	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.InCall != nil {
		t.Fatalf("a call followed by the session speaking again is not open: %+v", *v.InCall)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-11T04:37:37Z")) {
		t.Fatalf("got %s", v.LastSpoke)
	}
}

// A session that is sent a prompt and never answers it -- stuck on the model side
// before its first word -- has said nothing at all. Reporting that as "not measured"
// would exempt it from the silence rule forever, so its silence runs from the first
// line of the transcript: it has said nothing since then.
func TestVoiceWithNoSpeechCountsFromTheFirstLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p,
		untimed("mode"),
		userPrompt("2026-09-12T18:00:00Z", "start"),
		reminder("2026-09-12T18:00:01Z"),
		queuedMessage("2026-09-12T18:20:00Z"),
	)
	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:00:00Z")) {
		t.Fatalf("with nothing said, silence runs from the first timestamped line, got %s", v.LastSpoke)
	}
	if v.InCall != nil {
		t.Fatal("no tool_use, no call")
	}
}

// A subagent is the session working through someone else's transcript. While the call
// that started it is open, the subagent's own speech is the session speaking: an Agent
// call that runs for twenty minutes with its subagent busy throughout has not been
// silent for twenty minutes. The link is the subagent's meta file, which names the
// tool_use that spawned it.
func TestVoiceCountsTheSubagentOfTheOpenCall(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "11111111-1111-4111-8111-111111111111.jsonl")
	writeLines(t, p,
		assistantToolUse("2026-09-12T18:00:00Z", "agent1", "Agent"),
	)
	subs := filepath.Join(dir, "11111111-1111-4111-8111-111111111111", "subagents")
	writeLines(t, filepath.Join(subs, "agent-abc.jsonl"),
		userPrompt("2026-09-12T18:00:01Z", "do the thing"),
		assistantToolUse("2026-09-12T18:19:00Z", "x", "Bash"),
		toolResult("2026-09-12T18:19:30Z", "x"),
		queuedMessage("2026-09-12T18:25:00Z"),
	)
	writeLines(t, filepath.Join(subs, "agent-abc.meta.json"), `{"agentType":"general-purpose","toolUseId":"agent1"}`)

	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.InCall == nil || v.InCall.Tool != "Agent" {
		t.Fatalf("the session is still inside its Agent call, got %+v", v.InCall)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:19:30Z")) {
		t.Fatalf("the subagent's last word is the session's last word, got %s", v.LastSpoke)
	}
}

func TestVoiceIgnoresASubagentOfAnotherCall(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "22222222-2222-4222-8222-222222222222.jsonl")
	writeLines(t, p,
		assistantToolUse("2026-09-12T18:00:00Z", "agent2", "Agent"),
	)
	subs := filepath.Join(dir, "22222222-2222-4222-8222-222222222222", "subagents")
	writeLines(t, filepath.Join(subs, "agent-old.jsonl"),
		assistantText("2026-09-12T18:30:00Z", "from an earlier, finished call"),
	)
	writeLines(t, filepath.Join(subs, "agent-old.meta.json"), `{"toolUseId":"someOtherCall"}`)

	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:00:00Z")) {
		t.Fatalf("a subagent of a different call says nothing about this one, got %s", v.LastSpoke)
	}
}

func TestVoiceOnMissingFileIsTypedError(t *testing.T) {
	_, err := ReadVoice(filepath.Join(t.TempDir(), "gone.jsonl"))
	if !errors.Is(err, ErrNoTranscript) {
		t.Fatalf("want ErrNoTranscript, got %v", err)
	}
}

// An empty transcript and one with no timestamped line at all have nothing to measure
// from: a zero LastSpoke, which the caller reads as "not measured".
func TestVoiceWithNothingTimestampedIsZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p, untimed("mode"), untimed("custom-title"))
	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if !v.LastSpoke.IsZero() || v.InCall != nil {
		t.Fatalf("nothing timestamped, nothing measured: %+v", v)
	}
}
