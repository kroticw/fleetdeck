// Package transcript reads Claude Code session transcripts. It is the source
// for what a session did; it never reports what a session is doing now.
package transcript

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ErrNoTranscript means no usable transcript exists for the session.
var ErrNoTranscript = errors.New("transcript not found")

// sessionUUIDPattern is the only shape Locate accepts for a session
// identifier: a plain UUID, no path separators, no "..", no glob
// metacharacters. The identifier reaches this function from callers on the
// road to HTTP handlers that take a session id straight from a request, so
// it must never be trusted to interpolate safely into a filesystem glob.
var sessionUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Locate finds the transcript of a session by its UUID under the projects
// directory. sessionUUID must be a plain UUID; anything else — a glob
// pattern, a path with "..", an absolute path — is rejected as if no
// transcript existed, rather than being interpolated into the lookup.
//
// If more than one project directory holds a transcript for the same UUID
// (a session copied, or a project directory renamed), Locate picks the one
// with the newest modification time, so a stale copy under an old project
// path never wins over the current one.
func Locate(projectsDir, sessionUUID string) (string, error) {
	if !sessionUUIDPattern.MatchString(sessionUUID) {
		return "", fmt.Errorf("%w: %s", ErrNoTranscript, sessionUUID)
	}

	matches, err := filepath.Glob(filepath.Join(projectsDir, "*", sessionUUID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("glob transcripts: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNoTranscript, sessionUUID)
	}

	root, err := filepath.Abs(projectsDir)
	if err != nil {
		return "", fmt.Errorf("resolve projects dir: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve projects dir: %w", err)
	}

	chosen, chosenModTime := "", int64(-1)
	sort.Strings(matches) // deterministic tie-break when mod times are equal
	for _, m := range matches {
		abs, err := filepath.Abs(m)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// The glob pattern is anchored under projectsDir, so this only
			// fires if a symlink resolved outside it after the match — the
			// escape the glob itself cannot cause but a symlinked project
			// directory could.
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		if mt := info.ModTime().UnixNano(); mt > chosenModTime {
			chosen, chosenModTime = m, mt
		}
	}
	if chosen == "" {
		return "", fmt.Errorf("%w: %s", ErrNoTranscript, sessionUUID)
	}
	return chosen, nil
}
