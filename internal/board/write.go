package board

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	validStages   = map[string]bool{"new": true, "active": true, "review": true, "done": true, "blocked": true}
	validProgress = map[int]bool{0: true, 10: true, 20: true, 40: true, 60: true, 80: true, 100: true}
	startedStages = map[string]bool{"active": true, "review": true, "done": true, "blocked": true}
)

// SetField rewrites exactly one frontmatter line and leaves every other byte
// alone. The file is re-read here, immediately before the write, so the
// panel never writes from a copy of the card it rendered earlier. That does
// not make the write race-free: a line an agent appends to this same file
// between this re-read and the rename below is still lost, because the
// agents that append to a card do not coordinate with this process at all.
// Narrowing the window is all a re-read can do.
func SetField(path, field, value string) error {
	var normalized string
	switch field {
	case "stage":
		if !validStages[value] {
			return fmt.Errorf("unknown stage %q", value)
		}
		normalized = value
	case "progress":
		n, err := strconv.Atoi(value)
		if err != nil || !validProgress[n] {
			return fmt.Errorf("progress must be one of 0,10,20,40,60,80,100, got %q", value)
		}
		// Write the normalized number, never the caller's raw string: a
		// value like "0100" or "+100" passes strconv.Atoi but is not the
		// plain decimal literal YAML expects, and yaml.v3 resolves a
		// leading-zero scalar as octal on the next read.
		normalized = strconv.Itoa(n)
	default:
		return fmt.Errorf("%w: %s", ErrUnknownField, field)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read card before write: %w", err)
	}
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		return fmt.Errorf("card %s has no frontmatter to write into", path)
	}
	var fm frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		return fmt.Errorf("card %s has malformed frontmatter, refusing write: %w", path, err)
	}
	if err := checkCrossFieldRules(field, normalized, fm); err != nil {
		return err
	}

	out, err := substituteField(raw, field, normalized)
	if err != nil {
		return fmt.Errorf("card %s %w", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat card before write: %w", err)
	}
	return atomicWrite(path, out, info.Mode())
}

// substituteField replaces the value of the first line matching "field:..."
// inside raw's frontmatter block (never inside the body, where an agent's
// own text might coincidentally match the same pattern) and refuses if that
// would change the number of lines in the file — a real guard against a
// substitution eating or adding a line, not a formality.
func substituteField(raw []byte, field, value string) ([]byte, error) {
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("has no frontmatter block")
	}
	head := raw[:len(m[0])]
	line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(field) + `:.*$`)
	loc := line.FindIndex(head)
	if loc == nil {
		return nil, fmt.Errorf("has no %s field", field)
	}
	replacement := field + ": " + value
	out := make([]byte, 0, len(raw)+len(replacement))
	out = append(out, head[:loc[0]]...)
	out = append(out, replacement...)
	out = append(out, head[loc[1]:]...)
	out = append(out, raw[len(head):]...)
	if strings.Count(string(out), "\n") != strings.Count(string(raw), "\n") {
		return nil, fmt.Errorf("refusing to write: line count would change")
	}
	return out, nil
}

// checkCrossFieldRules keeps a card in a state the board's own validator
// accepts. Both rules are one-directional: progress 100 with a stage other
// than done is legal, so a card can reach done by having progress set to
// 100 first and stage set to done second.
func checkCrossFieldRules(field, value string, fm frontmatter) error {
	switch field {
	case "stage":
		if value == "done" && fm.Progress != 100 {
			return fmt.Errorf("cannot set stage to done while progress is %d: the board requires progress 100 at stage done", fm.Progress)
		}
		if startedStages[value] && fm.Session == "" {
			return fmt.Errorf("cannot set stage to %s while session is empty: the board requires a session at stage %s", value, value)
		}
	case "progress":
		if fm.Stage == "done" && value != "100" {
			return fmt.Errorf("cannot set progress to %s while stage is done: the board requires progress 100 at stage done", value)
		}
	}
	return nil
}

// atomicWrite writes data to path without ever leaving a truncated or empty
// file on disk: it writes to a temp file in the same directory, syncs it,
// preserves the original file's mode, then renames it over the original.
// The temp file is removed on any error before the rename.
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".board-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for atomic write: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file over card: %w", err)
	}
	cleanup = false
	return nil
}
