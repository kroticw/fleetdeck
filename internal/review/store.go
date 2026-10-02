package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
)

const commentsFile = "comments.json"

var (
	// ErrStale means the file moved on since the revision the write was made
	// against: another tab wrote first. Nothing was written.
	ErrStale     = errors.New("the review has changed since this page read it")
	ErrNotDraft  = errors.New("the comment was already sent to the session and can only be resolved")
	ErrNoComment = errors.New("no such comment")
	ErrNoDrafts  = errors.New("there are no draft comments to send")
)

// Dir is where one card's review lives: keyed by the card's number, which
// survives the card file being renamed.
func Dir(boardDir, cardID string) string {
	return filepath.Join(boardDir, "reviews", cardID)
}

type Comment struct {
	ID       string `json:"id"`
	Round    int    `json:"round"`
	ReplyTo  string `json:"replyTo"`
	Anchor   Anchor `json:"anchor"`
	Body     string `json:"body"`
	Created  string `json:"created"`
	Resolved bool   `json:"resolved"`
}

type Round struct {
	N         int                 `json:"n"`
	Sent      string              `json:"sent"`
	Head      string              `json:"head"`
	Positions map[string]Position `json:"positions"`
	Delivery  string              `json:"delivery"`
}

// Comments is the operator's file. Only the panel writes it; the agent reads
// it. Round 0 is a draft the agent is never pointed at.
type Comments struct {
	Version  int       `json:"version"`
	Card     string    `json:"card"`
	Rev      int       `json:"rev"`
	NextID   int       `json:"nextId"`
	Comments []Comment `json:"comments"`
	Rounds   []Round   `json:"rounds"`
}

func Load(dir string) (Comments, error) {
	raw, err := os.ReadFile(filepath.Join(dir, commentsFile))
	if errors.Is(err, os.ErrNotExist) {
		return Comments{Version: 1, NextID: 1}, nil
	}
	if err != nil {
		return Comments{}, err
	}
	var c Comments
	if err := json.Unmarshal(raw, &c); err != nil {
		return Comments{}, fmt.Errorf("%s: %w", filepath.Join(dir, commentsFile), err)
	}
	return c, nil
}

var locks sync.Map // dir -> *sync.Mutex

// Update applies fn to the file as it stands, provided it still stands at
// rev, and writes the result at rev+1 through a temporary file, so the agent
// never reads half of it.
func Update(dir, card string, rev int, fn func(*Comments) error) (Comments, error) {
	mu, _ := locks.LoadOrStore(dir, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	c, err := Load(dir)
	if err != nil {
		return Comments{}, err
	}
	if c.Rev != rev {
		return Comments{}, fmt.Errorf("%w: it is at revision %d, the page wrote against %d", ErrStale, c.Rev, rev)
	}
	if err := fn(&c); err != nil {
		return Comments{}, err
	}
	c.Card, c.Rev = card, c.Rev+1
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Comments{}, err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return Comments{}, err
	}
	return c, writeAtomically(filepath.Join(dir, commentsFile), append(raw, '\n'))
}

func writeAtomically(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".comments-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func (c *Comments) find(id string) (*Comment, error) {
	for i := range c.Comments {
		if c.Comments[i].ID == id {
			return &c.Comments[i], nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNoComment, id)
}

func (c *Comments) AddDraft(a Anchor, body, replyTo, created string) string {
	if c.NextID == 0 {
		c.NextID = 1
	}
	id := "c" + strconv.Itoa(c.NextID)
	c.NextID++
	c.Comments = append(c.Comments, Comment{ID: id, ReplyTo: replyTo, Anchor: a, Body: body, Created: created})
	return id
}

func (c *Comments) draft(id string) (*Comment, error) {
	cm, err := c.find(id)
	if err != nil {
		return nil, err
	}
	if cm.Round != 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotDraft, id)
	}
	return cm, nil
}

func (c *Comments) EditDraft(id, body string) error {
	cm, err := c.draft(id)
	if err != nil {
		return err
	}
	cm.Body = body
	return nil
}

func (c *Comments) DeleteDraft(id string) error {
	if _, err := c.draft(id); err != nil {
		return err
	}
	c.Comments = slices.DeleteFunc(c.Comments, func(cm Comment) bool { return cm.ID == id })
	return nil
}

func (c *Comments) SetResolved(id string, v bool) error {
	cm, err := c.find(id)
	if err != nil {
		return err
	}
	cm.Resolved = v
	return nil
}

func (c *Comments) Drafts() []Comment {
	var out []Comment
	for _, cm := range c.Comments {
		if cm.Round == 0 {
			out = append(out, cm)
		}
	}
	return out
}

// Freeze turns every draft into round n and records the head it was sent
// against with the positions traced on it: the one derived value the file
// keeps, labelled with the commit it was derived on.
func (c *Comments) Freeze(head, sent string, positions map[string]Position) (int, error) {
	if len(c.Drafts()) == 0 {
		return 0, ErrNoDrafts
	}
	n := len(c.Rounds) + 1
	for i := range c.Comments {
		if c.Comments[i].Round == 0 {
			c.Comments[i].Round = n
		}
	}
	c.Rounds = append(c.Rounds, Round{N: n, Sent: sent, Head: head, Positions: positions})
	return n, nil
}

func (c *Comments) SetDelivery(n int, delivery string) {
	for i := range c.Rounds {
		if c.Rounds[i].N == n {
			c.Rounds[i].Delivery = delivery
		}
	}
}
