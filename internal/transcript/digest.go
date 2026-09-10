package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

// Step is one readable moment of a session: a message that carries text.
type Step struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type rawLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// A message sent to a session that is mid-turn does not become a "user"
	// line at all. Claude Code absorbs it into the running turn and records it
	// here instead, as an attachment of type queued_command — see queued.
	Attachment struct {
		Type string `json:"type"`
		// CommandMode separates a person writing to the session ("prompt")
		// from the runtime doing so ("task-notification").
		CommandMode string          `json:"commandMode"`
		Prompt      json.RawMessage `json:"prompt"`
		// The moment the message was typed. The enclosing line is written when
		// the session got round to reading it, which is a different moment and
		// belongs to the session, not to the person.
		Timestamp string `json:"timestamp"`
	} `json:"attachment"`
}

// contentText extracts plain text from a field that is either a string or a
// block list. Adjacent text blocks are joined with a newline; concatenating
// them directly runs the words at the seam together.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (r rawLine) text() string { return contentText(r.Message.Content) }

// queued reports the message a person sent while the session was busy, the
// moment they sent it, and whether this line is such a message at all.
//
// This is the whole of a defect that made a panel lie for as long as it has
// existed. A message typed while the session is idle becomes a "user" line and
// is read below; a message typed while it is thinking is absorbed into the
// running turn and recorded only here, so it never reached the screen — not
// late, never. A person writes to a session precisely when it is busy, so what
// went missing was not a random sample of their words but the ones they got up
// to say. The complaint that reported this was itself absorbed this way, and
// was itself invisible.
//
// Only commandMode "prompt" is taken. A background task's notification is
// queued through the same door but is the runtime talking to the session; it
// reaches the feed on its own when it lands as a user line, and taking it from
// the queue as well would bury the thing this exists to show.
//
// Origin is deliberately not filtered. A message from a peer session is as
// invisible as one from the operator and for the same reason.
func (r rawLine) queued() (text, at string, ok bool) {
	if r.Type != "attachment" || r.Attachment.Type != "queued_command" || r.Attachment.CommandMode != "prompt" {
		return "", "", false
	}
	at = r.Attachment.Timestamp
	if at == "" {
		at = r.Timestamp
	}
	return contentText(r.Attachment.Prompt), at, true
}

// noisePrefixes marks a text block as Claude Code housekeeping rather than
// real conversation content: local-command envelopes, interruption markers,
// skill scaffolding notices. Ported from NOISE_PREFIXES in
// plugin/scripts/transcript_digest.py, the Python digest this list first
// came from — keep the two lists in sync so they can be compared line by
// line.
var noisePrefixes = []string{
	"<local-command",
	"<command-name",
	"<command-message",
	"Caveat:",
	"Base directory for this skill:",
	"[Request interrupted",
	// The preamble the CLI writes for itself when a conversation is compacted.
	// It is shaped exactly like something a person typed, and now that a user
	// step is attributed to the operator on screen, showing it would put the
	// runtime's own words in his mouth.
	"This session is being continued from a previous conversation",
}

// isNoise reports whether text is empty or a housekeeping envelope rather
// than a real conversational step.
func isNoise(text string) bool {
	stripped := strings.TrimLeft(text, " \t\r\n")
	if stripped == "" {
		return true
	}
	for _, prefix := range noisePrefixes {
		if strings.HasPrefix(stripped, prefix) {
			return true
		}
	}
	return false
}

// Digest returns up to limit of the most recent steps, oldest first. It reads
// the transcript from the tail, so answering a small request never requires
// loading a file of tens of megabytes in full. A non-positive limit means
// zero steps were asked for: it returns an empty result without opening the
// file, rather than being read as "no limit" and forcing a full read. An
// empty transcript is an error: nothing to look at is not the same as
// nothing found.
func Digest(path string, limit int) ([]Step, error) {
	if limit <= 0 {
		return nil, nil
	}

	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoTranscript, path)
	}
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat transcript: %w", err)
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrNoTranscript, path)
	}

	// newestFirst collects matched steps newest-to-oldest as the tail is walked
	// backwards; it is reversed once collection stops.
	var newestFirst []Step

	// A queued message that the session went on to read as a turn of its own is
	// in the transcript twice: once as the queued command, once as the user
	// line it became. Showing both would be worse than the defect being fixed —
	// a lost message is merely absent, a doubled one makes a person believe they
	// sent it twice.
	//
	// It counts rather than remembering a set, because someone who typed the
	// same short word twice while the session was busy said it twice and a set
	// would silently eat the second. The tail is walked newest-first, so the
	// user line a queued command became is always seen before the queued
	// command itself, which is what makes the count line up.
	takenAsUserLine := map[string]int{}

	visit := func(line []byte) bool {
		var r rawLine
		if json.Unmarshal(line, &r) != nil {
			return false
		}

		role, txt, stamp := "", "", r.Timestamp
		switch queuedText, queuedAt, isQueued := r.queued(); {
		case r.Type == "assistant" || r.Type == "user":
			role, txt = r.Type, r.text()
		case isQueued:
			if takenAsUserLine[queuedText] > 0 {
				takenAsUserLine[queuedText]--
				return false
			}
			// Whoever typed it is the person speaking, so it is a user step
			// like any other; only the place it was recorded differs.
			role, txt, stamp = "user", queuedText, queuedAt
		default:
			return false
		}

		if isNoise(txt) {
			return false
		}
		at, err := time.Parse(time.RFC3339, stamp)
		if err != nil {
			// No trustworthy timestamp: dropping the line is safer than
			// keeping it with a zero time indistinguishable from a real one,
			// which would corrupt the oldest-first ordering callers rely on.
			return false
		}
		if r.Type == "user" {
			takenAsUserLine[txt]++
		}
		newestFirst = append(newestFirst, Step{Role: role, Text: txt, At: at})
		return len(newestFirst) >= limit
	}
	if err := reverseLines(f, visit); err != nil {
		return nil, err
	}

	steps := make([]Step, len(newestFirst))
	for i, s := range newestFirst {
		steps[len(newestFirst)-1-i] = s
	}
	// File order is write order, and for a queued message the two differ: the
	// line is written when the session got round to reading it but carries the
	// moment it was typed. Reversing alone would therefore put it after answers
	// that came before it. Sorting on the times the steps actually carry is what
	// makes "oldest first" true rather than nearly true — stable, so steps
	// sharing a timestamp keep the order the transcript recorded them in.
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At.Before(steps[j].At) })
	return steps, nil
}
