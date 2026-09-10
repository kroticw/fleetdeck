package transcript

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
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

// A message typed while the session is busy never becomes a "user" line. Claude
// Code absorbs it into the running turn and records it as an attachment of type
// queued_command; the operator's own complaint that his messages were invisible
// was itself recorded that way and was itself invisible. Break it by restoring
// the plain `r.Type != "assistant" && r.Type != "user"` gate and this test fails
// with the typed line missing entirely.
func TestDigestShowsAMessageTypedWhileTheSessionWasBusy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "absorbed.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-09-09T00:00:01Z","message":{"role":"user","content":"count the files"}}`,
		`{"type":"attachment","timestamp":"2026-09-09T00:00:02Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"stop, do the other thing","origin":{"kind":"human"},"timestamp":"2026-09-09T00:00:02Z"}}`,
		`{"type":"queue-operation","operation":"remove","reason":"absorbed_mid_turn","timestamp":"2026-09-09T00:00:03Z","content":"stop, do the other thing"}`,
		`{"type":"assistant","timestamp":"2026-09-09T00:00:04Z","message":{"role":"assistant","content":[{"type":"text","text":"switching"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found *Step
	for i := range steps {
		if steps[i].Text == "stop, do the other thing" {
			found = &steps[i]
		}
	}
	if found == nil {
		t.Fatalf("the message typed while the session was busy is missing: %+v", steps)
	}
	if found.Role != "user" {
		t.Fatalf("a typed message is the person speaking; got role %q", found.Role)
	}
}

// It belongs where the person typed it, not where the session got round to
// reading it: those are different moments, and the line that carries the
// message is written at the later one.
//
// The fixture is written the way a real transcript is written — the queued
// command appears in the file AFTER the answer that preceded it, because that
// is when the session absorbed it, while the moment it was typed is earlier.
// So file order alone puts it in the wrong place, and only reading the time it
// carries puts it in the right one. Break it by taking the line's own timestamp
// instead of the attachment's, or by dropping the sort, and this test fails.
func TestDigestPlacesAQueuedMessageWhenItWasTyped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "when.jsonl")
	lines := []string{
		`{"type":"assistant","timestamp":"2026-09-09T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"first"}]}}`,
		`{"type":"assistant","timestamp":"2026-09-09T00:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"second"}]}}`,
		`{"type":"attachment","timestamp":"2026-09-09T00:00:09Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"typed early","origin":{"kind":"human"},"timestamp":"2026-09-09T00:00:02Z"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("want 3 steps, got %d: %+v", len(steps), steps)
	}
	if steps[1].Text != "typed early" {
		t.Fatalf("want the typed message between the two answers, got %q", steps[1].Text)
	}
}

// A queued message that the session did read as a turn of its own is in the
// transcript twice — once as the queued command, once as the user line. Showing
// both would be worse than the defect being fixed: a lost message is merely
// absent, a doubled one makes a person think they sent it twice. Break it by
// removing the pending-count check and this test fails with two steps.
func TestDigestDoesNotShowAQueuedMessageTwice(t *testing.T) {
	p := filepath.Join(t.TempDir(), "twice.jsonl")
	lines := []string{
		`{"type":"attachment","timestamp":"2026-09-09T00:00:01Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"do the thing","origin":{"kind":"human"},"timestamp":"2026-09-09T00:00:01Z"}}`,
		`{"type":"user","timestamp":"2026-09-09T00:00:02Z","message":{"role":"user","content":"do the thing"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("want the message once, got %d: %+v", len(steps), steps)
	}
}

// The de-duplication counts, it does not remember a set of texts. Someone who
// typed the same short word twice while the session was busy said it twice, and
// a set would silently eat the second. Break it by keeping a set of seen texts
// instead of a count and this test fails with one step.
func TestDigestShowsBothOfTwoIdenticalQueuedMessages(t *testing.T) {
	p := filepath.Join(t.TempDir(), "repeat.jsonl")
	lines := []string{
		`{"type":"attachment","timestamp":"2026-09-09T00:00:01Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"да","origin":{"kind":"human"},"timestamp":"2026-09-09T00:00:01Z"}}`,
		`{"type":"attachment","timestamp":"2026-09-09T00:00:02Z","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"да","origin":{"kind":"human"},"timestamp":"2026-09-09T00:00:02Z"}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 {
		t.Fatalf("want both, got %d: %+v", len(steps), steps)
	}
}

// A background task's notification is queued the same way but is the runtime
// talking to the session, not a person writing to it. It reaches the feed on
// its own when it lands as a user line; taking it from the queue as well would
// bury the thing this change exists to show. Break it by dropping the
// commandMode check and this test fails with the notification as a step.
func TestDigestIgnoresQueuedTaskNotifications(t *testing.T) {
	p := filepath.Join(t.TempDir(), "notification.jsonl")
	lines := []string{
		`{"type":"attachment","timestamp":"2026-09-09T00:00:01Z","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"<task-notification><status>completed</status></task-notification>","timestamp":"2026-09-09T00:00:01Z"}}`,
		`{"type":"assistant","timestamp":"2026-09-09T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"noted"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Text != "noted" {
		t.Fatalf("want only the answer, got %d: %+v", len(steps), steps)
	}
}

// Attachments are most of what a transcript holds — token reminders, file
// contents, hook output, skill listings. Only a queued command is somebody
// speaking. Break it by matching on the line type alone and this test fails
// with a token reminder rendered as a message.
func TestDigestIgnoresAttachmentsThatAreNotQueuedCommands(t *testing.T) {
	p := filepath.Join(t.TempDir(), "attachments.jsonl")
	lines := []string{
		`{"type":"attachment","timestamp":"2026-09-09T00:00:01Z","attachment":{"type":"total_tokens_reminder","prompt":"14000000 tokens left"}}`,
		`{"type":"attachment","timestamp":"2026-09-09T00:00:02Z","attachment":{"type":"file","prompt":"package main"}}`,
		`{"type":"assistant","timestamp":"2026-09-09T00:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"noted"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Text != "noted" {
		t.Fatalf("want only the answer, got %d: %+v", len(steps), steps)
	}
}

// The preamble the CLI writes for itself when a conversation is compacted
// arrives shaped exactly like something a person typed. It mattered little
// while every user step looked the same; now that a user step is attributed to
// the operator on screen, leaving it in would put the runtime's own words in
// his mouth. Break it by removing the prefix from noisePrefixes and this test
// fails with the preamble as a step.
func TestDigestDropsTheCompactionPreamble(t *testing.T) {
	p := filepath.Join(t.TempDir(), "compaction.jsonl")
	preamble := "This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion of the conversation."
	lines := []string{
		`{"type":"user","timestamp":"2026-09-09T00:00:01Z","message":{"role":"user","content":` + strconv.Quote(preamble) + `}}`,
		`{"type":"assistant","timestamp":"2026-09-09T00:00:02Z","message":{"role":"assistant","content":[{"type":"text","text":"carrying on"}]}}`,
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := Digest(p, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Text != "carrying on" {
		t.Fatalf("want only the answer, got %d: %+v", len(steps), steps)
	}
}
