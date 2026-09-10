// Package board reads and writes the fleet board: markdown cards with YAML
// frontmatter. The panel owns two fields, stage and progress. Everything else
// belongs to the agents.
package board

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	// ErrNoCards means the board's cards subdirectory holds no cards at all.
	ErrNoCards = errors.New("no cards found")
	// ErrNoCardsDir means the board directory has no cards subdirectory —
	// distinct from ErrNoCards so an operator whose board.path names the
	// wrong directory entirely gets a different message than one whose
	// board is genuinely, correctly empty.
	ErrNoCardsDir = errors.New("board directory has no cards subdirectory")
	// ErrUnknownField means a write targeted a field the panel does not own.
	ErrUnknownField = errors.New("field is not writable")
	// ErrNothingToCommit means Commit found nothing staged for the given file.
	ErrNothingToCommit = errors.New("nothing to commit")

	frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n`)
	linkRe        = regexp.MustCompile(`\[\[([^\[\]\r\n]+?)\]\]`)
	titleRe       = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

type Card struct {
	Path       string   `json:"path"`
	Zone       string   `json:"zone"`
	Stage      string   `json:"stage"`
	Progress   int      `json:"progress"`
	Session    string   `json:"session"`
	Repo       string   `json:"repo"`
	Created    string   `json:"created"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Links      []string `json:"links"`
	ParseError string   `json:"parseError,omitempty"`
}

type frontmatter struct {
	Zone     string `yaml:"zone"`
	Stage    string `yaml:"stage"`
	Progress int    `yaml:"progress"`
	Session  string `yaml:"session"`
	Repo     string `yaml:"repo"`
	Created  string `yaml:"created"`
}

// ParseCard reads one card. A malformed card comes back with ParseError set:
// one bad card must not blind the board to the others.
func ParseCard(path string) (Card, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Card{}, fmt.Errorf("read card: %w", err)
	}
	c := Card{Path: path}
	m := frontmatterRe.FindSubmatch(raw)
	if m == nil {
		c.ParseError = "no frontmatter block"
		return c, nil
	}
	var fm frontmatter
	if err := yaml.Unmarshal(m[1], &fm); err != nil {
		c.ParseError = "frontmatter: " + err.Error()
		return c, nil
	}
	c.Zone, c.Stage, c.Progress = fm.Zone, fm.Stage, fm.Progress
	c.Session, c.Repo, c.Created = fm.Session, fm.Repo, fm.Created
	c.Body = string(raw[len(m[0]):])

	// Title and links are pulled from the body with fenced code regions
	// blanked out first, so a fenced snippet's "# comment" or "[[link]]"
	// never leaks into the card's kanban title or link list. Card.Body
	// itself stays the untouched original.
	stripped := stripFencedCode(c.Body)
	if t := titleRe.FindStringSubmatch(stripped); t != nil {
		c.Title = strings.TrimSpace(t[1])
	}
	for _, l := range linkRe.FindAllStringSubmatch(stripped, -1) {
		c.Links = append(c.Links, trimLinkTarget(l[1]))
	}
	return c, nil
}

// stripFencedCode returns body with every fenced code region (delimited by a
// line whose trimmed form starts with ``` or ~~~) replaced by nothing, so
// title and link extraction never look inside a fence. An unterminated fence
// drops everything from its opening marker to the end of the body.
func stripFencedCode(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// trimLinkTarget turns a raw [[...]] capture into the note name: everything
// up to the first "#" (heading) or "|" (alias) is dropped, so
// "[[note#heading]]" and "[[note|alias]]" both yield "note".
func trimLinkTarget(raw string) string {
	if i := strings.IndexAny(raw, "#|"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}
