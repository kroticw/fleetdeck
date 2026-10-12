package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
)

// T-161: with the board's commit queue running, a write answers once the file
// is on disk, and the commit follows. These run against a real repository whose
// commits are made slow by a pre-commit hook, so "before the commit" is a
// window wide enough to look into.

const slowCommit = 600 * time.Millisecond

// slowBoard is a board with cards/c.md committed, whose every later commit
// takes slowCommit, and the panel's queues running on it.
func slowBoard(t *testing.T) (dir, card string) {
	t.Helper()
	dir = initRepo(t)
	cards := board.CardsDir(dir)
	if err := os.MkdirAll(cards, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSampleCard(t, cards)
	card = filepath.Join(cards, "c.md")
	git(t, dir, "add", "--", "cards/c.md")
	git(t, dir, "commit", "--quiet", "--message", "seed")
	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 0.6\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	q := newCommitQueues(ctx, nil)
	boardQueues = q
	t.Cleanup(func() {
		wctx, wcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer wcancel()
		q.wait(wctx)
		cancel()
		boardQueues = nil
	})
	return dir, card
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitsIn(t *testing.T, dir string) int {
	t.Helper()
	return len(strings.Split(git(t, dir, "log", "--format=%s"), "\n"))
}

func settle(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	boardQueues.wait(ctx)
}

func TestAQueuedWriteAnswersBeforeItsCommitAndIsCommittedAfter(t *testing.T) {
	dir, card := slowBoard(t)
	start := time.Now()
	if err := setCardField(card, "stage", "review", nil); err != nil {
		t.Fatalf("write: %v", err)
	}
	if took := time.Since(start); took > slowCommit/2 {
		t.Fatalf("the write waited %v for its commit", took)
	}
	if raw, _ := os.ReadFile(card); !strings.Contains(string(raw), "stage: review") {
		t.Fatal("the answer came before the file was written")
	}
	settle(t)
	if got := git(t, dir, "log", "-1", "--format=%s"); got != "chore(board): set stage to review" {
		t.Fatalf("subject %q", got)
	}
}

func TestAQueuedCardAnswersBeforeItsCommitAndIsCommittedAfter(t *testing.T) {
	dir, _ := slowBoard(t)
	start := time.Now()
	path, err := createCard(dir, board.NewCard{Title: "A task", Zone: "planned"}, time.Now())
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if took := time.Since(start); took > slowCommit/2 {
		t.Fatalf("the card waited %v for its commit", took)
	}
	settle(t)
	if got := git(t, dir, "log", "-1", "--format=%s"); got != "chore(board): add card "+filepath.Base(path) {
		t.Fatalf("subject %q", got)
	}
}

// The dispatch writes the card's session and then its stage, and sends the
// task last (#225). With the queue, the task no longer waits for two commits;
// the commits still come, as one that names both fields.
func TestADispatchDeliversItsTaskWithoutWaitingForTheCommits(t *testing.T) {
	dir, card := slowBoard(t)
	// The sample card names a session: a dispatch is for a card with none.
	raw, _ := os.ReadFile(card)
	if err := os.WriteFile(card, []byte(strings.NewReplacer("session: abc12345", "session: \"\"", "stage: active", "stage: new", "repo: x/y", "repo: \"\"").Replace(string(raw))), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "commit", "--quiet", "--all", "--message", "free the card")
	before := commitsIn(t, dir)

	var mu sync.Mutex
	var sentAt time.Time
	d := &orchestrator.Dispatcher{
		Start: func(context.Context, string, string) (string, error) { return "5e55c0de", nil },
		List: func(context.Context) ([]daemon.Session, error) {
			mu.Lock()
			defer mu.Unlock()
			return []daemon.Session{{Short: "5e55c0de"}}, nil
		},
		Send: func(context.Context, string, string) error {
			mu.Lock()
			defer mu.Unlock()
			sentAt = time.Now()
			return nil
		},
		SetField: setCardField,
		Home:     t.TempDir(),
	}
	start := time.Now()
	res, err := d.Dispatch(context.Background(), orchestrator.Work{Card: card, Lang: "en"})
	if err != nil || !res.OK {
		t.Fatalf("dispatch: %v %+v", err, res)
	}
	mu.Lock()
	took := sentAt.Sub(start)
	mu.Unlock()
	if took > slowCommit/2 {
		t.Fatalf("the task waited %v, behind the card's commits", took)
	}
	settle(t)
	if got := commitsIn(t, dir) - before; got != 1 {
		t.Fatalf("%d commits, want the two writes as one", got)
	}
	if got := git(t, dir, "log", "-1", "--format=%s"); got != "chore(board): set session to 5e55c0de, stage to active" {
		t.Fatalf("subject %q", got)
	}
}

// A card moved into done tells its session and tidies it away (stagebyhand).
// Neither waits for the move's commit any more.
func TestAMoveIntoDoneTellsTheSessionWhileItsCommitIsStillBeingMade(t *testing.T) {
	dir, card := slowBoard(t)
	var told, tidied bool
	d := server.Deps{
		Snapshot:      func() state.Snapshot { return state.Snapshot{} },
		BoardDir:      board.CardsDir(dir),
		SetCardField:  setCardField,
		QueuedCommits: true,
		SendToSession: func(context.Context, string, string) error {
			told = true
			return nil
		},
		CleanupSession: func(context.Context, orchestrator.Accepted) (orchestrator.Result, error) {
			tidied = true
			return orchestrator.Result{OK: true}, nil
		},
	}
	before := commitsIn(t, dir)
	r := httptest.NewRequest(http.MethodPatch, "/api/cards", strings.NewReader(`{"path":"c.md","field":"stage","value":"done","lang":"en"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.New(d).ServeHTTP(rec, r)
	if rec.Code >= 300 {
		t.Fatalf("move: %d %s", rec.Code, rec.Body.String())
	}
	if !told || !tidied {
		t.Fatalf("told %v, tidied %v", told, tidied)
	}
	if got := commitsIn(t, dir); got != before {
		t.Fatal("the move waited for its commit")
	}
	settle(t)
	if got := commitsIn(t, dir); got != before+1 {
		t.Fatalf("the move was never committed: %d commits", got-before)
	}
	_ = card
}

// Without the queue — a test, or a panel wired without one — writes commit as
// they always did.
func TestWithoutAQueueAWriteIsCommittedBeforeItAnswers(t *testing.T) {
	if boardQueues != nil {
		t.Fatal("a queue left behind by another test")
	}
	dir := initRepo(t)
	writeSampleCard(t, dir)
	git(t, dir, "add", "--", "c.md")
	git(t, dir, "commit", "--quiet", "--message", "seed")
	if err := setCardField(filepath.Join(dir, "c.md"), "stage", "review", nil); err != nil {
		t.Fatal(err)
	}
	if got := git(t, dir, "log", "-1", "--format=%s"); got != "chore(board): set stage to review" {
		t.Fatalf("subject %q", got)
	}
}

// Every write asks for a fresh snapshot, and a collect can take hundreds of
// milliseconds. Ten writes during one collect are one more collect after it,
// not ten.
func TestWritesDuringARefreshAreOneMoreRefresh(t *testing.T) {
	release := make(chan struct{})
	var runs atomic.Int32
	// One cycle at a time, as panel.refresh holds cycleMu.
	var cycle sync.Mutex
	refresh := func(context.Context) {
		cycle.Lock()
		defer cycle.Unlock()
		if runs.Add(1) == 1 {
			<-release
		}
	}
	kick := soon(context.Background(), refresh)
	kick()
	for runs.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	for range 10 {
		kick()
		time.Sleep(2 * time.Millisecond)
	}
	close(release)
	time.Sleep(200 * time.Millisecond)
	if got := runs.Load(); got != 2 {
		t.Fatalf("%d refreshes, want the one running and one more", got)
	}
	kick()
	time.Sleep(100 * time.Millisecond)
	if got := runs.Load(); got != 3 {
		t.Fatalf("a write after the refreshes settled was %d refreshes, want one", got-2)
	}
}
