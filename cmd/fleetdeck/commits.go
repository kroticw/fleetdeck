package main

import (
	"context"
	"log"
	"path/filepath"
	"sync"

	"github.com/kroticw/fleetdeck/internal/board"
)

// boardQueues is the panel's board commit queues (board.Committer), one per
// board, started by serve. Nil — a test, or a panel not serving — and a write
// commits before it answers, as it always did.
var boardQueues *commitQueues

// commitQueues is a board.Committer per board directory, each made the first
// time its board is written and running until ctx ends.
type commitQueues struct {
	ctx     context.Context
	changed func()

	mu sync.Mutex
	by map[string]*board.Committer
}

func newCommitQueues(ctx context.Context, changed func()) *commitQueues {
	return &commitQueues{ctx: ctx, changed: changed, by: map[string]*board.Committer{}}
}

// key is a board directory as one queue knows it. A card's path arrives
// resolved (the server confines it with EvalSymlinks) and the board's own
// path as configured: both must reach the same queue, or two would race for
// one repository's index.
func key(boardDir string) string {
	if resolved, err := filepath.EvalSymlinks(boardDir); err == nil {
		return resolved
	}
	return filepath.Clean(boardDir)
}

func (q *commitQueues) of(boardDir string) *board.Committer {
	k := key(boardDir)
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.by[k]
	if !ok {
		c = board.NewCommitter(k, log.Printf, q.changed)
		q.by[k] = c
		go c.Run(q.ctx)
	}
	return c
}

// state is server.Deps.CommitState: the queue of the board at boardDir, empty
// for a board not written since the panel started.
func (q *commitQueues) state(boardDir string) board.CommitState {
	q.mu.Lock()
	c, ok := q.by[key(boardDir)]
	q.mu.Unlock()
	if !ok {
		return board.CommitState{}
	}
	return c.State()
}

// retry is server.Deps.RetryCommits.
func (q *commitQueues) retry(boardDir string) {
	q.of(boardDir).Retry()
}

// wait returns once every queue is idle, or when ctx ends: a panel shutting
// down gives its queues that long to finish.
func (q *commitQueues) wait(ctx context.Context) {
	q.mu.Lock()
	all := make([]*board.Committer, 0, len(q.by))
	for _, c := range q.by {
		all = append(all, c)
	}
	q.mu.Unlock()
	for _, c := range all {
		if err := c.Wait(ctx); err != nil {
			log.Printf("board commits: %v; what is still queued stays written and uncommitted in the board", err)
			return
		}
	}
}

// cardInBoard is the board a card at path belongs to and the card relative to
// it: <board>/cards/<card>.md, or the card's own directory for a card outside
// a cards directory.
func cardInBoard(path string) (boardDir, rel string) {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "cards" {
		return filepath.Dir(dir), filepath.Join("cards", filepath.Base(path))
	}
	return dir, filepath.Base(path)
}

// soon runs refresh in the background, one run at a time: asked again while a
// run is going, it runs once more after it, however many times it was asked.
// A burst of writes is a refresh or two, not one each — a collect can take
// hundreds of milliseconds, and each waiting run would be a whole one.
func soon(ctx context.Context, refresh func(context.Context)) func() {
	var mu sync.Mutex
	running, again := false, false
	return func() {
		mu.Lock()
		if running {
			again = true
			mu.Unlock()
			return
		}
		running = true
		mu.Unlock()
		go func() {
			for {
				refresh(ctx)
				mu.Lock()
				if !again {
					running = false
					mu.Unlock()
					return
				}
				again = false
				mu.Unlock()
			}
		}()
	}
}
