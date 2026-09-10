package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
}

// text extracts plain text from a content field that is either a string or a
// block list. Adjacent text blocks are joined with a newline; concatenating
// them directly runs the words at the seam together.
func (r rawLine) text() string {
	var s string
	if json.Unmarshal(r.Message.Content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
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
	visit := func(line []byte) bool {
		var r rawLine
		if json.Unmarshal(line, &r) != nil {
			return false
		}
		if r.Type != "assistant" && r.Type != "user" {
			return false
		}
		txt := r.text()
		if isNoise(txt) {
			return false
		}
		at, err := time.Parse(time.RFC3339, r.Timestamp)
		if err != nil {
			// No trustworthy timestamp: dropping the line is safer than
			// keeping it with a zero time indistinguishable from a real one,
			// which would corrupt the oldest-first ordering callers rely on.
			return false
		}
		newestFirst = append(newestFirst, Step{Role: r.Type, Text: txt, At: at})
		return len(newestFirst) >= limit
	}
	if err := reverseLines(f, visit); err != nil {
		return nil, err
	}

	steps := make([]Step, len(newestFirst))
	for i, s := range newestFirst {
		steps[len(newestFirst)-1-i] = s
	}
	return steps, nil
}
