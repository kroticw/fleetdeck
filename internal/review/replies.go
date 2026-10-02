package review

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const repliesFile = "replies.md"

var (
	replyID     = regexp.MustCompile(`^c\d+$`)
	replyStatus = map[string]bool{"fixed": true, "declined": true, "question": true}
)

// Reply is one "## <id>" section of the agent's file. A section the panel
// cannot read keeps Parsed false and is shown as Raw, never hidden.
type Reply struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Commit  string `json:"commit"`
	Text    string `json:"text"`
	Raw     string `json:"raw"`
	Parsed  bool   `json:"parsed"`
	Partial bool   `json:"partial"`
}

// LoadReplies reads the agent's file. The panel never writes it, and reads
// it as possibly half-written: how the agent's editor writes is not known.
func LoadReplies(dir string) ([]Reply, error) {
	raw, err := os.ReadFile(filepath.Join(dir, repliesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseReplies(string(raw)), nil
}

func ParseReplies(raw string) []Reply {
	var out []Reply
	sections := strings.Split("\n"+raw, "\n## ")
	for _, s := range sections[1:] {
		out = append(out, parseReply("## "+s))
	}
	// An editor writing in place can be read mid-write; a file that does not
	// end its last line has not finished it.
	if len(out) > 0 && !strings.HasSuffix(raw, "\n") {
		out[len(out)-1].Partial = true
	}
	return out
}

func parseReply(section string) Reply {
	r := Reply{Raw: strings.TrimRight(section, "\n")}
	head, body, _ := strings.Cut(section, "\n")
	r.ID = strings.TrimSpace(strings.TrimPrefix(head, "## "))
	fields, text, _ := strings.Cut(body, "\n\n")
	for _, line := range strings.Split(fields, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "status":
			r.Status = strings.TrimSpace(value)
		case "commit":
			r.Commit = strings.TrimSpace(value)
		}
	}
	r.Text = strings.TrimSpace(text)
	r.Parsed = replyID.MatchString(r.ID) && replyStatus[r.Status]
	return r
}
