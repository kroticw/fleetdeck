package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// text extracts plain text from a content field that is either a string or a block list.
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
	out := ""
	for _, b := range blocks {
		if b.Type == "text" {
			out += b.Text
		}
	}
	return out
}

// Digest returns up to limit of the most recent steps, oldest first. It reads
// the transcript from the tail, so answering a small request never requires
// loading a file of tens of megabytes in full. An empty transcript is an
// error: nothing to look at is not the same as nothing found.
func Digest(path string, limit int) ([]Step, error) {
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
		if txt == "" {
			return false
		}
		at, _ := time.Parse(time.RFC3339, r.Timestamp)
		newestFirst = append(newestFirst, Step{Role: r.Type, Text: txt, At: at})
		return limit > 0 && len(newestFirst) >= limit
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
