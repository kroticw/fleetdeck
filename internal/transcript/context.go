package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// Usage is the context occupancy of a session. Estimated is true when the number
// came from the transcript rather than from Claude Code itself.
type Usage struct {
	Tokens    int  `json:"tokens"`
	Window    int  `json:"window"`
	Estimated bool `json:"estimated"`
}

// windows maps a model name to its context window. Unknown models fall back to 200k,
// the smallest window in current use, so an unrecognized future model is never
// reported as having more headroom than it might actually have.
var windows = map[string]int{
	"claude-opus-5":    1_000_000,
	"claude-sonnet-5":  1_000_000,
	"claude-fable-5-1": 1_000_000,
	"claude-haiku-4-5": 200_000,
}

// ContextUsage estimates context occupancy from the transcript's last response
// usage, as the fallback path for when the statusline reporter is absent: the
// sum of cache_read_input_tokens, cache_creation_input_tokens and input_tokens
// over the model's window. It reads from the tail so the search for that last
// usage block does not require loading a large transcript in full. The result
// is always marked Estimated, since it never comes from Claude Code itself.
func ContextUsage(path string) (Usage, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Usage{}, fmt.Errorf("%w: %s", ErrNoTranscript, path)
	}
	if err != nil {
		return Usage{}, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	var last struct {
		Model string `json:"model"`
		Usage struct {
			Input         int `json:"input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	found := false
	visit := func(line []byte) bool {
		var envelope struct {
			Message json.RawMessage `json:"message"`
		}
		if json.Unmarshal(line, &envelope) != nil || len(envelope.Message) == 0 {
			return false
		}
		var probe struct {
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal(envelope.Message, &probe) != nil || len(probe.Usage) == 0 {
			return false
		}
		if json.Unmarshal(envelope.Message, &last) != nil {
			return false
		}
		found = true
		return true // The tail's first match is the last response; stop there.
	}
	if err := reverseLines(f, visit); err != nil {
		return Usage{}, err
	}
	if !found {
		return Usage{}, fmt.Errorf("no usage data in %s", path)
	}

	window, ok := windows[last.Model]
	if !ok {
		window = 200_000
	}
	return Usage{
		Tokens:    last.Usage.Input + last.Usage.CacheCreation + last.Usage.CacheRead,
		Window:    window,
		Estimated: true,
	}, nil
}
