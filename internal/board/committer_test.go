package board

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The panel answers a card write as soon as the file is on disk and records it
// in the board's history afterwards, one commit at a time (T-161). A commit is
// a signed git commit, about 0.4 s, and up to 30 s when the signing key's PIN
// is not cached: the operator no longer waits for it, and nothing written is
// lost when it fails.

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// boardRepo is a board with one committed card, cards/T-001.md.
func boardRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := initRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "cards"), 0o755); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(dir, "cards", "T-001.md")
	if err := os.WriteFile(card, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "--", "cards/T-001.md")
	gitOut(t, dir, "commit", "--quiet", "--message", "seed")
	return dir, card
}

func edit(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(sample+text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func start(t *testing.T, c *Committer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
}

func settled(t *testing.T, c *Committer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Wait(ctx); err != nil {
		t.Fatalf("the queue never settled: %v", err)
	}
}

func subjects(t *testing.T, dir string) []string {
	t.Helper()
	return strings.Split(gitOut(t, dir, "log", "--format=%s"), "\n")
}

func TestACommitIsMadeAfterTheWriteWithTheSameMessageAndASignoff(t *testing.T) {
	dir, card := boardRepo(t)
	c := NewCommitter(dir, nil, nil)
	start(t, c)
	edit(t, card, "\nmoved\n")
	c.SetField("cards/T-001.md", "stage", "review")
	settled(t, c)
	if got := subjects(t, dir)[0]; got != "chore(board): set stage to review" {
		t.Fatalf("subject %q", got)
	}
	if body := gitOut(t, dir, "log", "-1", "--format=%B"); !strings.Contains(body, "Signed-off-by:") {
		t.Fatalf("the commit lost its signoff:\n%s", body)
	}
	if st := gitOut(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("the card was left uncommitted: %s", st)
	}
}

// Writes to one card before its commit runs are one commit, and the commit
// says them all: the history says what the file holds.
func TestWritesToOneCardAreOneCommitNamingEveryField(t *testing.T) {
	dir, card := boardRepo(t)
	c := NewCommitter(dir, nil, nil)
	edit(t, card, "\nboth\n")
	c.SetField("cards/T-001.md", "stage", "review")
	c.SetField("cards/T-001.md", "progress", "80")
	c.SetField("cards/T-001.md", "stage", "done")
	start(t, c)
	settled(t, c)
	got := subjects(t, dir)
	if got[0] != "chore(board): set stage to done, progress to 80" || len(got) != 2 {
		t.Fatalf("history %q", got)
	}
}

// Writes to one card waiting together are one commit even with another card's
// write between them: the first commit of the file takes everything written
// to it, so its message must name all of it.
func TestWritesToOneCardWaitingTogetherAreOneCommitWhateverIsBetween(t *testing.T) {
	dir, card := boardRepo(t)
	other := filepath.Join(dir, "cards", "T-002.md")
	c := NewCommitter(dir, nil, nil)
	edit(t, card, "\na\n")
	if err := os.WriteFile(other, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c.SetField("cards/T-001.md", "session", "abc12345")
	c.AddCard("T-002.md", "cards/T-002.md")
	c.SetField("cards/T-001.md", "stage", "active")
	start(t, c)
	settled(t, c)
	got := subjects(t, dir)[:2]
	want := []string{"chore(board): add card T-002.md", "chore(board): set session to abc12345, stage to active"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("history %q, want %q", got, want)
	}
}

// An agent or the orchestrator commits the board with its own git. Holding the
// index lock is a moment's business: the commit waits for it rather than
// failing.
func TestACommitWaitsOutAnotherGitHoldingTheIndex(t *testing.T) {
	dir, card := boardRepo(t)
	lock := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(lock)
	}()
	c := NewCommitter(dir, nil, nil)
	start(t, c)
	edit(t, card, "\nlocked\n")
	c.SetField("cards/T-001.md", "stage", "review")
	settled(t, c)
	if st := c.State(); st.Failed != 0 {
		t.Fatalf("a lock held for a moment left a failure: %+v", st)
	}
	if got := subjects(t, dir)[0]; got != "chore(board): set stage to review" {
		t.Fatalf("subject %q", got)
	}
}

// The orchestrator's own commit can take the panel's file with it before the
// queue gets there. Nothing is left to commit, and nothing went wrong.
func TestAFileAnotherCommitAlreadyTookIsNoFailure(t *testing.T) {
	dir, card := boardRepo(t)
	c := NewCommitter(dir, nil, nil)
	edit(t, card, "\ntaken\n")
	c.SetField("cards/T-001.md", "stage", "review")
	gitOut(t, dir, "add", "--all")
	gitOut(t, dir, "commit", "--quiet", "--message", "orchestrator tidies the fleet")
	start(t, c)
	settled(t, c)
	if st := c.State(); st.Failed != 0 || st.Queued != 0 {
		t.Fatalf("state %+v", st)
	}
}

// --- a commit that fails ---

type fakeCommits struct {
	mu    sync.Mutex
	made  []string
	fails int
}

func (f *fakeCommits) commit(_ string, message string, _ ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.made = append(f.made, message)
	if f.fails > 0 {
		f.fails--
		return errors.New("git commit timed out after 30s, likely a commit-signing passphrase prompt with no one to answer it")
	}
	return nil
}

func (f *fakeCommits) tried() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.made...)
}

func TestAFailedCommitIsDebtThatIsLoggedAndShown(t *testing.T) {
	var logged []string
	var mu sync.Mutex
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	f := &fakeCommits{fails: 1}
	c := NewCommitter("/board", logf, nil)
	c.commit = f.commit
	start(t, c)
	c.SetField("cards/T-001.md", "stage", "review")
	settled(t, c)
	st := c.State()
	if st.Failed != 1 || !strings.Contains(st.Error, "passphrase") {
		t.Fatalf("the failure is not said: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "cards/T-001.md") || !strings.Contains(logged[0], "set stage to review") || !strings.Contains(logged[0], "passphrase") {
		t.Fatalf("the panel's log does not say what was left uncommitted: %q", logged)
	}
}

// A failed commit is tried again only when the operator is there to answer a
// PIN prompt: at the next write, or when asked. Never by a timer.
func TestDebtIsTriedAgainAtTheNextWriteFirstAndNeverByItself(t *testing.T) {
	f := &fakeCommits{fails: 1}
	c := NewCommitter("/board", nil, nil)
	c.commit = f.commit
	start(t, c)
	c.SetField("cards/T-001.md", "stage", "review")
	settled(t, c)
	time.Sleep(300 * time.Millisecond)
	if got := f.tried(); len(got) != 1 {
		t.Fatalf("a failed commit was tried again with nobody asking: %q", got)
	}
	c.SetField("cards/T-002.md", "stage", "blocked")
	settled(t, c)
	got := f.tried()
	if fmt.Sprint(got) != fmt.Sprint([]string{"chore(board): set stage to review", "chore(board): set stage to review", "chore(board): set stage to blocked"}) {
		t.Fatalf("commits tried %q", got)
	}
	if st := c.State(); st.Failed != 0 || st.Error != "" {
		t.Fatalf("paid debt is still shown: %+v", st)
	}
}

func TestDebtIsTriedAgainWhenAsked(t *testing.T) {
	f := &fakeCommits{fails: 1}
	c := NewCommitter("/board", nil, nil)
	c.commit = f.commit
	start(t, c)
	c.SetField("cards/T-001.md", "stage", "review")
	settled(t, c)
	c.Retry()
	settled(t, c)
	if st := c.State(); st.Failed != 0 {
		t.Fatalf("a retry left the debt: %+v", st)
	}
	if got := f.tried(); len(got) != 2 {
		t.Fatalf("commits tried %q", got)
	}
}

func TestEveryChangeOfTheQueueIsAnnounced(t *testing.T) {
	var mu sync.Mutex
	told := 0
	c := NewCommitter("/board", nil, func() {
		mu.Lock()
		defer mu.Unlock()
		told++
	})
	c.commit = (&fakeCommits{}).commit
	start(t, c)
	c.SetField("cards/T-001.md", "stage", "review")
	settled(t, c)
	mu.Lock()
	defer mu.Unlock()
	if told < 2 {
		t.Fatalf("the queue changed twice (queued, committed) and said so %d times", told)
	}
}
