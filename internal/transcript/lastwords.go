package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// ErrNoWords is a transcript holding nothing the session itself said. It is
// separate from ErrNoTranscript because the two lead somewhere different: a
// missing file is a session whose history cannot be found, and this is a
// session that was started and never answered anything.
var ErrNoWords = errors.New("the transcript holds nothing the session said")

// LastWords is the newest thing the session said in its own voice, cut to max
// runes. It is what a session leaves behind when it is put out: the answer it
// finished on, which for a worker session is its hand-over.
//
// Only an assistant text block counts. A tool call is the session working and
// its result is the world answering; thinking is the model's own working and
// was never addressed to anyone. Walking back past all three is the difference
// between an archive entry carrying the session's conclusion and one carrying
// the tail of a shell command.
//
// It reads from the tail and stops at the first answer, so a transcript tens of
// megabytes long costs one chunk of it.
func LastWords(path string, limit int) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: %s", ErrNoTranscript, path)
	}
	if err != nil {
		return "", fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	var said string
	visit := func(line []byte) bool {
		var r struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &r) != nil || r.Type != "assistant" {
			return false
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(r.Message.Content, &blocks) != nil {
			return false
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, strings.TrimSpace(b.Text))
			}
		}
		if len(parts) == 0 {
			return false
		}
		said = strings.Join(parts, "\n")
		return true
	}
	if err := reverseLines(f, visit); err != nil {
		return "", err
	}
	if said == "" {
		return "", fmt.Errorf("%w: %s", ErrNoWords, path)
	}
	return cut(said, limit), nil
}

// cut trims text to max runes, never splitting one, and says so when it did.
func cut(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}
