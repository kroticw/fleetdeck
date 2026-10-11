package board

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Committer records the panel's writes to one board in the board's git
// history, after the write has been answered (T-161).
//
// A card write used to answer only once its commit was made: a signed git
// commit, about 0.4 s, and up to gitTimeout when the signing key's PIN is not
// cached. The file is what the board is, and it is written — atomically — before
// the write answers; the commit is queued here and made one at a time, in the
// order the writes were made, with the messages and signoff Commit always gave
// them.
//
// Writes to one card waiting together are one commit, and its message names
// every field: the commit records what the file holds, and the history says
// so.
//
// A commit that fails is debt: logged with its paths, message and error, said
// in State, and tried again — before anything newer — at the next write or
// when Retry is asked. Never by a timer: the likeliest failure is a PIN prompt
// nobody answered, and a timer would raise it again with nobody there. Debt
// lives in memory only; the files it names stay written and uncommitted in the
// board's tree, as any failed commit always left them.
type Committer struct {
	dir     string
	logf    func(format string, args ...any)
	changed func()
	// commit is commitPaths behind the index lock's patience; a test replaces it.
	commit func(dir, message string, paths ...string) error

	mu       sync.Mutex
	queue    []commitJob
	debt     []commitJob
	lastErr  string
	retry    bool
	inflight int
	busy     bool
	wake     chan struct{}
}

// CommitState is what a board's queue has to say: commits waiting or being
// made, commits that failed and wait to be tried again, and the last failure.
type CommitState struct {
	Queued int    `json:"queued,omitempty"`
	Failed int    `json:"failed,omitempty"`
	Error  string `json:"error,omitempty"`
}

type commitField struct{ name, value string }

type commitJob struct {
	paths  []string
	card   string        // a card added, by file name
	fields []commitField // fields set, in the order first written
}

func (j commitJob) message() string {
	parts := make([]string, len(j.fields))
	for i, f := range j.fields {
		parts[i] = f.name + " to " + f.value
	}
	set := "set " + strings.Join(parts, ", ")
	switch {
	case j.card == "":
		return "chore(board): " + set
	case len(j.fields) == 0:
		return "chore(board): add card " + j.card
	default:
		// A card written to before its start was committed: the commit reads the
		// file with the write in it.
		return "chore(board): add card " + j.card + ", " + set
	}
}

// file is the card a job is about, relative to the board: a start's own file
// comes first among its paths.
func (j commitJob) file() string { return j.paths[0] }

// NewCommitter is the queue for the board at dir. logf is where debt is said
// (nowhere when nil), and changed is told whenever State changes.
func NewCommitter(dir string, logf func(format string, args ...any), changed func()) *Committer {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if changed == nil {
		changed = func() {}
	}
	return &Committer{dir: dir, logf: logf, changed: changed, commit: commitPatiently, wake: make(chan struct{}, 1)}
}

// SetField queues the commit of a field written to the card at path, relative
// to the board.
func (c *Committer) SetField(path, field, value string) {
	c.enqueue(commitJob{paths: []string{path}, fields: []commitField{{field, value}}})
}

// AddCard queues the commit of a card started on the board: name is its file
// name, paths the card and its attachments, relative to the board.
func (c *Committer) AddCard(name string, paths ...string) {
	c.enqueue(commitJob{paths: paths, card: name})
}

// Retry tries the debt again now.
func (c *Committer) Retry() {
	c.mu.Lock()
	c.retry = true
	c.mu.Unlock()
	c.poke()
}

func (c *Committer) enqueue(j commitJob) {
	c.mu.Lock()
	c.queue = append(c.queue, j)
	c.mu.Unlock()
	c.changed()
	c.poke()
}

func (c *Committer) poke() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// State is the queue as it stands.
func (c *Committer) State() CommitState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CommitState{Queued: len(c.queue) + c.inflight, Failed: len(c.debt), Error: c.lastErr}
}

// Run makes the commits until ctx is done. A panel shutting down waits for the
// queue first (Wait), for as long as it can afford.
func (c *Committer) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			// A dispatch writes a card's session and its stage a moment apart:
			// a short wait makes them one batch, and one commit.
			select {
			case <-ctx.Done():
				return
			case <-time.After(gather):
			}
			c.drain()
		}
	}
}

// Wait returns once nothing is queued or being committed, or with ctx's error.
func (c *Committer) Wait(ctx context.Context) error {
	for {
		c.mu.Lock()
		idle := !c.busy && len(c.queue) == 0 && !c.retry
		c.mu.Unlock()
		if idle {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (c *Committer) drain() {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 && !c.retry {
			c.mu.Unlock()
			return
		}
		// Debt first: it was written first.
		batch := coalesce(append(c.debt, c.queue...))
		c.debt, c.queue, c.retry = nil, nil, false
		c.inflight, c.busy = len(batch), true
		c.mu.Unlock()

		var failed []commitJob
		var lastErr string
		for _, j := range batch {
			// Writes to this card made since the batch was taken are in the file
			// the commit is about to read: their fields go into its message.
			c.mu.Lock()
			absorbed := 0
			j, c.queue, absorbed = absorb(j, c.queue)
			c.inflight += absorbed
			c.mu.Unlock()
			err := c.commit(c.dir, j.message(), j.paths...)
			// Nothing to commit: another commit — the orchestrator's, tidying the
			// fleet — took the file first, or it already held what was written.
			if err != nil && !errors.Is(err, ErrNothingToCommit) {
				failed = append(failed, j)
				lastErr = err.Error()
				c.logf("board commit failed, left uncommitted: %s (%s): %v", strings.Join(j.paths, ", "), j.message(), err)
			}
			c.mu.Lock()
			c.inflight -= 1 + absorbed
			c.mu.Unlock()
			c.changed()
		}

		c.mu.Lock()
		c.debt = failed
		c.lastErr = lastErr
		c.busy = false
		c.mu.Unlock()
		c.changed()
	}
}

// coalesce makes the writes to one card waiting together one commit, in the
// place of the first of them, each field at the last value written to it, in
// the order it was first written. Not only writes in a row: the first commit
// of a file takes everything written to it so far, and its message must say
// all of it. A card's start takes the writes to the card that follow it; a
// start is never taken into anything.
func coalesce(jobs []commitJob) []commitJob {
	var out []commitJob
	first := map[string]int{}
	for _, j := range jobs {
		if i, ok := first[j.file()]; ok && j.card == "" {
			out[i].fields = mergeFields(out[i].fields, j.fields)
			continue
		}
		j.fields = append([]commitField(nil), j.fields...)
		first[j.file()] = len(out)
		out = append(out, j)
	}
	return out
}

// absorb takes into j every queued write to the same card, and returns what is
// left of the queue and how many it took. A start is never taken.
//
// shortcut: a write landing between this and git reading the file is
// committed under the earlier message, and its own commit finds nothing; the
// window is the few milliseconds before git commit starts.
func absorb(j commitJob, queue []commitJob) (commitJob, []commitJob, int) {
	rest := queue[:0:0]
	taken := 0
	for _, q := range queue {
		if q.card == "" && q.file() == j.file() {
			j.fields = mergeFields(j.fields, q.fields)
			taken++
			continue
		}
		rest = append(rest, q)
	}
	return j, rest, taken
}

func mergeFields(into, more []commitField) []commitField {
	for _, f := range more {
		found := false
		for i := range into {
			if into[i].name == f.name {
				into[i].value, found = f.value, true
			}
		}
		if !found {
			into = append(into, f)
		}
	}
	return into
}

// lockWaits is how long a commit waits, each time, for another git holding
// the board's index lock — an agent's or the orchestrator's commit — before it
// is debt. A var only so a test can shorten it.
var lockWaits = []time.Duration{200 * time.Millisecond, 500 * time.Millisecond, time.Second}

// gather is how long the queue waits, once woken, for the writes that come
// with the first: a dispatch's session and stage, a stage and its progress.
const gather = 30 * time.Millisecond

func commitPatiently(dir, message string, paths ...string) error {
	// A card gone before its commit came — archived and committed by the
	// orchestrator, or deleted — has nothing to record: git would fail on it
	// at every try, or record its deletion under this message.
	if _, err := os.Stat(filepath.Join(dir, paths[0])); errors.Is(err, fs.ErrNotExist) {
		return ErrNothingToCommit
	}
	err := commitPaths(dir, message, paths...)
	for _, wait := range lockWaits {
		if err == nil || !strings.Contains(err.Error(), "index.lock") {
			return err
		}
		time.Sleep(wait)
		err = commitPaths(dir, message, paths...)
	}
	return err
}
