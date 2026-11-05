package board

import (
	"io"
	"os"

	"github.com/kroticw/fleetdeck/internal/executor"
	"gopkg.in/yaml.v3"
)

// docHead is how much of a document is read for its frontmatter. The list of
// documents is built for every card the panel opens, so the whole file is not.
const docHead = 8 << 10

// DocSession is the session key of the author of the document at path,
// from the document's own frontmatter (docs/en/board-convention.md, "Documents
// a card links to"), or "" when the document names none or names it wrongly.
// A wrong value is dropped rather than passed on: the panel would open a
// session that does not exist, or someone else's.
func DocSession(path string) string {
	f, err := os.Open(path) //nolint:gosec // the path comes from the docs listing, already confined to a root
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head, err := io.ReadAll(io.LimitReader(f, docHead))
	if err != nil {
		return ""
	}
	m := frontmatterRe.FindSubmatch(head)
	if m == nil {
		return ""
	}
	var fm struct {
		Session string `yaml:"session"`
	}
	if yaml.Unmarshal(m[1], &fm) != nil {
		return ""
	}
	_, key, err := executor.ParseKey(fm.Session)
	if err != nil {
		return ""
	}
	return string(key)
}
