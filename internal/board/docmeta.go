package board

import (
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// docHead is how much of a document is read for its frontmatter. The list of
// documents is built for every card the panel opens, so the whole file is not.
const docHead = 8 << 10

// shortIDRe is the form of a session's short id: the one the board validator
// holds a card's and a document's session to (validate_cards.py, SESSION_RE).
var shortIDRe = regexp.MustCompile(`^[0-9a-fA-F]{6,12}$`)

// DocSession is the short id of the session that wrote the document at path,
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
	if yaml.Unmarshal(m[1], &fm) != nil || !shortIDRe.MatchString(fm.Session) {
		return ""
	}
	return fm.Session
}
