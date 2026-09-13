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

func queuedCommand(at string) string {
	return `{"type":"attachment","timestamp":"` + at + `","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"are you there?"}}`
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

func readVoiceOf(t *testing.T, lines ...string) Voice {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, p, lines...)
	v, err := ReadVoice(p)
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
	v := readVoiceOf(t,
		userPrompt("2026-09-10T19:40:00Z", "go"),
		assistantToolUse("2026-09-10T19:47:28Z", "t1", "Bash"),
		toolResult("2026-09-10T19:47:47Z", "t1"),
		reminder("2026-09-10T19:47:47Z"),
		assistantToolUse("2026-09-10T19:48:08Z", "t2", "mcp__claude-agents__send_message"),
		queuedMessage("2026-09-10T19:53:49Z"),
		untimed("last-prompt"),
		untimed("cost-state"),
	)
	if want := at(t, "2026-09-10T19:48:08Z"); !v.LastSpoke.Equal(want) {
		t.Fatalf("the last thing the session said was opening the call at %s; got %s", want, v.LastSpoke)
	}
	if !v.Unanswered.Equal(at(t, "2026-09-10T19:48:08Z")) {
		t.Fatalf("a call that never came back is unanswered from the moment it was made, got %s", v.Unanswered)
	}
}

func TestVoiceReportsTheCallTheSessionIsStandingIn(t *testing.T) {
	v := readVoiceOf(t,
		assistantThinking("2026-09-12T18:00:00Z"),
		assistantToolUse("2026-09-12T18:00:01Z", "r1", "Read"),
		queuedMessage("2026-09-12T18:05:00Z"),
	)
	if v.InCall == nil {
		t.Fatal("an unanswered tool_use at the tail is a call the session is standing in")
	}
	if v.InCall.Tool != "Read" || !v.InCall.Since.Equal(at(t, "2026-09-12T18:00:01Z")) {
		t.Fatalf("want Read since 18:00:01, got %+v", *v.InCall)
	}
}

// A call that came back leaves the session owing the model's next move. That is the
// shape a transcript is left in while a call is still running but Claude Code has not
// written it yet -- measured on 2.1.269: a Read, alone or beside a Bash, reaches the
// file only together with its result, so for the whole call the file ends at the
// previous result. From outside, "the model is thinking" and "a call nobody can see is
// hanging" are this one shape, and the time it has lasted is what can be measured.
func TestVoiceAResultWithNothingSaidSinceIsUnanswered(t *testing.T) {
	v := readVoiceOf(t,
		assistantToolUse("2026-09-12T18:00:00Z", "a", "Bash"),
		toolResult("2026-09-12T18:00:02Z", "a"),
		reminder("2026-09-12T18:00:02Z"),
	)
	if v.InCall != nil {
		t.Fatalf("every call has its result, so no call is named: %+v", *v.InCall)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:00:02Z")) {
		t.Fatalf("the result is the last thing said, got %s", v.LastSpoke)
	}
	if !v.Unanswered.Equal(at(t, "2026-09-12T18:00:02Z")) {
		t.Fatalf("the session owes its next move since the result came back, got %s", v.Unanswered)
	}
}

// Parallel calls are written as one tool_use line each, and their results come back
// one by one. The call still open is the one with no result, wherever it sits in the
// batch.
func TestVoiceFindsTheOpenCallInAParallelBatch(t *testing.T) {
	v := readVoiceOf(t,
		toolResult("2026-09-12T17:59:00Z", "earlier"),
		assistantToolUse("2026-09-12T18:00:00Z", "a", "Read"),
		assistantToolUse("2026-09-12T18:00:00Z", "b", "Bash"),
		assistantToolUse("2026-09-12T18:00:01Z", "c", "Grep"),
		toolResult("2026-09-12T18:00:02Z", "c"),
		toolResult("2026-09-12T18:00:03Z", "b"),
	)
	if v.InCall == nil || v.InCall.Tool != "Read" {
		t.Fatalf("Read is the one call in the batch with no result, got %+v", v.InCall)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:00:03Z")) {
		t.Fatalf("a sibling's result arriving is the session hearing back, got %s", v.LastSpoke)
	}
}

// The shape of 5bc37caf after it was resumed: the call it froze in never got a result,
// because the process holding it died, and the session carried on after the resume.
// That call is history, not something it is standing in now, and the session that
// spoke last owes nobody anything.
func TestVoiceASessionThatSpokeLastOwesNothing(t *testing.T) {
	v := readVoiceOf(t,
		assistantToolUse("2026-09-10T19:48:08Z", "s", "mcp__claude-agents__send_message"),
		queuedMessage("2026-09-10T19:53:49Z"),
		userPrompt("2026-09-11T04:37:37Z", "Continue from where you left off."),
		assistantText("2026-09-11T04:37:37Z", "No response requested."),
		untimed("turn-duration"),
	)
	if v.InCall != nil {
		t.Fatalf("a call followed by the session speaking again is not open: %+v", *v.InCall)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-11T04:37:37Z")) {
		t.Fatalf("got %s", v.LastSpoke)
	}
	if !v.Unanswered.IsZero() {
		t.Fatalf("its own line is the newest, so it owes nothing: got %s", v.Unanswered)
	}
}

// A session that finished its turn and was then written to owes an answer from the
// moment it was written to -- not from the end of its turn, which may be hours ago
// and would make every idle session look long unanswered the instant a message lands.
func TestVoiceAMessageToASessionThatHadFinishedIsUnansweredFromTheMessage(t *testing.T) {
	queued := readVoiceOf(t,
		assistantText("2026-09-12T15:00:00Z", "done"),
		queuedMessage("2026-09-12T18:00:00Z"),
	)
	if !queued.Unanswered.Equal(at(t, "2026-09-12T18:00:00Z")) {
		t.Fatalf("a queued message is unanswered from when it was sent, got %s", queued.Unanswered)
	}
	if !queued.LastSpoke.Equal(at(t, "2026-09-12T15:00:00Z")) {
		t.Fatalf("and it is not the session speaking, got %s", queued.LastSpoke)
	}

	typed := readVoiceOf(t,
		assistantText("2026-09-12T15:00:00Z", "done"),
		userPrompt("2026-09-12T18:00:00Z", "next"),
		reminder("2026-09-12T18:00:00Z"),
	)
	if !typed.Unanswered.Equal(at(t, "2026-09-12T18:00:00Z")) {
		t.Fatalf("a typed prompt is unanswered from when it was typed, got %s", typed.Unanswered)
	}
}

// Claude Code writes the queued-command record of a message it delivered mid-turn into
// the transcript later, still stamped with the moment the message was sent. A copy
// like that landing after the session's final words is not a new address: it is older
// than what the session last said.
func TestVoiceAnOlderQueuedRecordAfterTheSessionsLastWordsOwesNothing(t *testing.T) {
	v := readVoiceOf(t,
		assistantText("2026-09-12T18:10:00Z", "all done"),
		queuedCommand("2026-09-12T18:05:00Z"),
	)
	if !v.Unanswered.IsZero() {
		t.Fatalf("a record older than the session's last words addresses nobody now, got %s", v.Unanswered)
	}
}

// A session that is sent a prompt and never answers it -- stuck on the model side
// before its first word -- has said nothing at all. Reporting that as "not measured"
// would exempt it from the silence rule forever, so its silence runs from the first
// line of the transcript, and so does what it leaves unanswered.
func TestVoiceWithNoSpeechCountsFromTheFirstLine(t *testing.T) {
	v := readVoiceOf(t,
		untimed("mode"),
		userPrompt("2026-09-12T18:00:00Z", "start"),
		reminder("2026-09-12T18:00:01Z"),
		queuedMessage("2026-09-12T18:20:00Z"),
	)
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:00:00Z")) {
		t.Fatalf("with nothing said, silence runs from the first timestamped line, got %s", v.LastSpoke)
	}
	if !v.Unanswered.Equal(at(t, "2026-09-12T18:00:00Z")) {
		t.Fatalf("the first prompt is what it has left unanswered, got %s", v.Unanswered)
	}
	if v.InCall != nil {
		t.Fatal("no tool_use, no call")
	}
}

// A subagent is the session working through someone else's transcript. While the
// session owes its next move, a subagent of its speaking is the session speaking: an
// Agent call that runs for twenty minutes with its subagent busy throughout has not
// been silent for twenty minutes. Every subagent counts, not only the one linked to a
// visible call, because the call that started it may be one Claude Code has not
// written into the file yet.
func TestVoiceCountsTheSessionsSubagents(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "11111111-1111-4111-8111-111111111111.jsonl")
	writeLines(t, p,
		assistantToolUse("2026-09-12T17:59:00Z", "prev", "Bash"),
		toolResult("2026-09-12T18:00:00Z", "prev"),
	)
	subs := filepath.Join(dir, "11111111-1111-4111-8111-111111111111", "subagents")
	writeLines(t, filepath.Join(subs, "agent-abc.jsonl"),
		userPrompt("2026-09-12T18:00:05Z", "do the thing"),
		assistantToolUse("2026-09-12T18:19:00Z", "x", "Bash"),
		toolResult("2026-09-12T18:19:30Z", "x"),
		queuedMessage("2026-09-12T18:25:00Z"),
	)

	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:19:30Z")) {
		t.Fatalf("the subagent's last word is the session's last word, got %s", v.LastSpoke)
	}
	if !v.Unanswered.Equal(at(t, "2026-09-12T18:19:30Z")) {
		t.Fatalf("and the unanswered stretch restarts with it, got %s", v.Unanswered)
	}
}

// A subagent that went quiet before the session's own last line says nothing new.
func TestVoiceASubagentOlderThanTheSessionChangesNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "22222222-2222-4222-8222-222222222222.jsonl")
	writeLines(t, p,
		assistantToolUse("2026-09-12T18:30:00Z", "agent2", "Agent"),
	)
	subs := filepath.Join(dir, "22222222-2222-4222-8222-222222222222", "subagents")
	writeLines(t, filepath.Join(subs, "agent-old.jsonl"),
		assistantText("2026-09-12T18:00:00Z", "from an earlier, finished call"),
	)

	v, err := ReadVoice(p)
	if err != nil {
		t.Fatal(err)
	}
	if !v.LastSpoke.Equal(at(t, "2026-09-12T18:30:00Z")) {
		t.Fatalf("an older subagent line cannot move the session's voice back, got %s", v.LastSpoke)
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
	v := readVoiceOf(t, untimed("mode"), untimed("custom-title"))
	if !v.LastSpoke.IsZero() || v.InCall != nil || !v.Unanswered.IsZero() {
		t.Fatalf("nothing timestamped, nothing measured: %+v", v)
	}
}
