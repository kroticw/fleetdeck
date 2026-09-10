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
	// ErrNoCards means the board directory holds no cards at all.
	ErrNoCards = errors.New("no cards found")
	// ErrUnknownField means a write targeted a field the panel does not own.
	ErrUnknownField = errors.New("field is not writable")

	frontmatterRe = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	linkRe        = regexp.MustCompile(`\[\[([^\]|#]+)`)
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
	if t := titleRe.FindStringSubmatch(c.Body); t != nil {
		c.Title = strings.TrimSpace(t[1])
	}
	for _, l := range linkRe.FindAllStringSubmatch(c.Body, -1) {
		c.Links = append(c.Links, strings.TrimSpace(l[1]))
	}
	return c, nil
}
