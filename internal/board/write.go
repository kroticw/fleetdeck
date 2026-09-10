package board

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	validStages   = map[string]bool{"new": true, "active": true, "review": true, "done": true, "blocked": true}
	validProgress = map[int]bool{0: true, 10: true, 20: true, 40: true, 60: true, 80: true, 100: true}
)

// SetField rewrites exactly one frontmatter line and leaves every other byte
// alone. The file is re-read here, immediately before the write, so a log line
// an agent appended after the panel rendered the card survives.
func SetField(path, field, value string) error {
	switch field {
	case "stage":
		if !validStages[value] {
			return fmt.Errorf("unknown stage %q", value)
		}
	case "progress":
		n, err := strconv.Atoi(value)
		if err != nil || !validProgress[n] {
			return fmt.Errorf("progress must be one of 0,10,20,40,60,80,100, got %q", value)
		}
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
	head := string(raw[:len(m[0])])
	line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(field) + `:.*$`)
	if !line.MatchString(head) {
		return fmt.Errorf("card %s has no %s field", path, field)
	}
	out := line.ReplaceAllString(head, field+": "+value) + string(raw[len(m[0]):])
	if strings.Count(out, "\n") != strings.Count(string(raw), "\n") {
		return fmt.Errorf("refusing to write: line count would change in %s", path)
	}
	return os.WriteFile(path, []byte(out), 0o600)
}
