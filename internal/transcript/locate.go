// Package transcript reads Claude Code session transcripts. It is the source
// for what a session did; it never reports what a session is doing now.
package transcript

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ErrNoTranscript means no usable transcript exists for the session.
var ErrNoTranscript = errors.New("transcript not found")

// Locate finds the transcript of a session by its UUID under the projects directory.
func Locate(projectsDir, sessionUUID string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", sessionUUID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("glob transcripts: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNoTranscript, sessionUUID)
	}
	return matches[0], nil
}
