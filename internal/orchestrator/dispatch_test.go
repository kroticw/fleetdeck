package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

const workCard = `---
id: T-042
zone: planned
stage: new
progress: 0
session: ""
repo: work/proj
created: 2026-09-23
---

# Дотащить доску до перетаскивания
`

// fakeWorker records what a dispatch did, in the order it did it.
type fakeWorker struct {
	steps   []string
	listed  map[string]bool
	startAs string
	startTo error
	sendErr error
	setErr  error
	// firstErrs are returned by SendFirst one per call, then nil.
	firstErrs []error
}

func newWorker() *fakeWorker {
	return &fakeWorker{listed: map[string]bool{}, startAs: "abc12345"}
}

func (f *fakeWorker) start(_ context.Context, cwd, name string) (string, error) {
	f.steps = append(f.steps, "start:"+cwd+":"+name)
	if f.startTo != nil {
		return "", f.startTo
	}
	f.listed[f.startAs] = true
	return f.startAs, nil
}

func (f *fakeWorker) list(context.Context) ([]daemon.Session, error) {
	out := make([]daemon.Session, 0, len(f.listed))
	for short := range f.listed {
		out = append(out, daemon.Session{Short: short})
	}
	return out, nil
}

func (f *fakeWorker) send(_ context.Context, short, text string) error {
	f.steps = append(f.steps, "send:"+short+":"+text)
	return f.sendErr
}

func (f *fakeWorker) first(_ context.Context, short, text string) error {
	f.steps = append(f.steps, "first:"+short+":"+text)
	if len(f.firstErrs) == 0 {
		return nil
	}
	err := f.firstErrs[0]
	f.firstErrs = f.firstErrs[1:]
	return err
}

func (f *fakeWorker) set(path, field, value string, expect *string) error {
	was := "<any>"
	if expect != nil {
		was = *expect
	}
	f.steps = append(f.steps, "set:"+filepath.Base(path)+":"+field+"="+value+":was="+was)
	return f.setErr
}

func dispatcher(t *testing.T, f *fakeWorker) (*Dispatcher, string) {
	t.Helper()
	dir, home := t.TempDir(), t.TempDir()
	card := filepath.Join(dir, "T-042-card.md")
	if err := os.WriteFile(card, []byte(workCard), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "work", "proj"), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Dispatcher{
		Start:     f.start,
		List:      f.list,
		Send:      f.send,
		SendFirst: f.first,
		SetField:  f.set,
		Home:      home,
		Poll:      time.Millisecond,
		StartWait: 200 * time.Millisecond,
		SendWait:  200 * time.Millisecond,
	}, card
}

// The order is the whole point of this path: a session with no prompt, its
// short id written into the card, and only then the task. An agent told to
// work a card whose session field is still empty writes its own id into it,
// and the panel's write and the agent's diverge.
func TestDispatchStartsTheSessionWritesTheIDThenSendsTheTask(t *testing.T) {
	f := newWorker()
	d, card := dispatcher(t, f)

	res, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("dispatch did not finish: %+v", res.Steps)
	}
	if res.Session != "abc12345" {
		t.Fatalf("session = %q", res.Session)
	}
	want := []string{
		"start:" + filepath.Join(d.Home, "work", "proj") + ":T-042 Дотащить доску до перетаскивания",
		"set:T-042-card.md:session=abc12345:was=",
		"set:T-042-card.md:stage=active:was=new",
		"first:abc12345:" + Task("en", card),
	}
	if strings.Join(f.steps, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant\n%s", strings.Join(f.steps, "\n"), strings.Join(want, "\n"))
	}
}

// The task is the first message into a session started with no prompt, and
// on a fleet whose Send acknowledges a message its session has not read yet,
// SendFirst is the send that waits for the session to take it. A fleet whose
// Send is enough gives none, and the task goes by Send.
func TestDispatchSendsTheTaskBySendWhenThereIsNoSendFirst(t *testing.T) {
	f := newWorker()
	d, card := dispatcher(t, f)
	d.SendFirst = nil
	res, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("dispatch did not finish: %+v", res.Steps)
	}
	if last := f.steps[len(f.steps)-1]; last != "send:abc12345:"+Task("en", card) {
		t.Fatalf("the task went by %q", last)
	}
}

// A session that acknowledged the task and never took it is the failure this
// path exists to make loud: the card names the session, the session runs, and
// the task is nowhere. The step is refused by name, once — the task is not sent
// again, since a text sitting unsubmitted in the prompt would then go in twice.
func TestDispatchRefusesTheTaskStepWhenTheSessionDidNotTakeIt(t *testing.T) {
	f := newWorker()
	f.firstErrs = []error{&daemon.ErrNotTaken{Session: "abc12345", Reason: "the reply was acknowledged, but its list record never showed the text taken"}}
	d, card := dispatcher(t, f)

	res, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("a task the session never took was reported delivered")
	}
	if res.Session != "abc12345" {
		t.Fatalf("the started session must be named in the result, got %q", res.Session)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Name != "task" || last.Error == "" {
		t.Fatalf("the refused step must be the task: %+v", res.Steps)
	}
	if !strings.Contains(last.Error, "abc12345") || !strings.Contains(last.Error, Task("en", card)) {
		t.Fatalf("the refusal must name the session and carry the task to send by hand: %q", last.Error)
	}
	sends := 0
	for _, step := range f.steps {
		if strings.HasPrefix(step, "first:") || strings.HasPrefix(step, "send:") {
			sends++
		}
	}
	if sends != 1 {
		t.Fatalf("the task was sent %d times, want once", sends)
	}
}

// A session still coming up is asked again, as an appointment's is.
func TestDispatchAsksAgainWhileTheSessionIsNotTakingTheTask(t *testing.T) {
	f := newWorker()
	f.firstErrs = []error{&daemon.ErrStarting{}, &daemon.ErrNoreply{}}
	d, card := dispatcher(t, f)
	res, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("not ok: %+v", res.Steps)
	}
	if got := strings.Count(strings.Join(f.steps, "\n"), "first:"); got != 3 {
		t.Fatalf("asked %d times, want three", got)
	}
}

// The message is one line for the reason the appointment's is: a reply the
// session does not submit is left sitting in its prompt.
func TestTheTaskIsOneLineNamingTheCard(t *testing.T) {
	text := Task("en", "/b/cards/T-042-card.md")
	if strings.Contains(text, "\n") {
		t.Fatalf("the task must be one line: %q", text)
	}
	if !strings.Contains(text, "/b/cards/T-042-card.md") {
		t.Fatalf("the task must name the card: %q", text)
	}
}

// A worker is spoken to in the page's language, as the orchestrator is by the
// wizard, and an unknown language falls back to English rather than nothing.
func TestTheTaskIsInThePagesLanguage(t *testing.T) {
	en, ru := Task("en", "/b/c.md"), Task("ru", "/b/c.md")
	if en == ru {
		t.Fatalf("the Russian task is the English one: %q", ru)
	}
	if !strings.Contains(ru, "/b/c.md") || strings.Contains(ru, "\n") {
		t.Fatalf("the Russian task must be one line naming the card: %q", ru)
	}
	if Task("xx", "/b/c.md") != en {
		t.Fatalf("an unknown language must fall back to English: %q", Task("xx", "/b/c.md"))
	}
}

// A card that already names a session is a card somebody is working. Starting a
// second session for it would leave the first one running with no card naming
// it, which is what the board calls an orphan.
func TestDispatchRefusesACardThatAlreadyNamesASession(t *testing.T) {
	f := newWorker()
	d, card := dispatcher(t, f)
	if err := os.WriteFile(card, []byte(strings.Replace(workCard, `session: ""`, "session: deadbeef", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
	if !errors.Is(err, ErrCardTaken) {
		t.Fatalf("want ErrCardTaken, got %v", err)
	}
	if len(f.steps) != 0 {
		t.Fatalf("a refused dispatch did something: %v", f.steps)
	}
}

// A worker works in the repository its card names, under the home directory.
// A card that names none, or names one that is not there, is refused before
// anything starts: a session started somewhere else works the wrong checkout,
// and nothing on the panel says so.
func TestDispatchRefusesACardWithNoRepositoryToWorkIn(t *testing.T) {
	for _, tc := range []struct{ name, repo, want string }{
		{"missing directory", "repo: work/gone", "work/gone"},
		{"a file, not a directory", "repo: work/file", "not a directory"},
		{"outside home", "repo: ../elsewhere", "inside the home directory"},
		{"absolute", "repo: /etc", "inside the home directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWorker()
			d, card := dispatcher(t, f)
			if err := os.WriteFile(filepath.Join(d.Home, "work", "file"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(card, []byte(strings.Replace(workCard, "repo: work/proj", tc.repo, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
			if !errors.Is(err, ErrNoRepo) {
				t.Fatalf("want ErrNoRepo, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal must say why: %q lacks %q", err, tc.want)
			}
			if len(f.steps) != 0 {
				t.Fatalf("a refused dispatch did something: %v", f.steps)
			}
		})
	}
}

// The field is written as the board README says, a path from the home
// directory; "~/" in front is how a person spells the same thing.
func TestDispatchReadsATildeRepoAsFromHome(t *testing.T) {
	f := newWorker()
	d, card := dispatcher(t, f)
	if err := os.WriteFile(card, []byte(strings.Replace(workCard, "repo: work/proj", "repo: ~/work/proj", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	if want := "start:" + filepath.Join(d.Home, "work", "proj") + ":T-042 Дотащить доску до перетаскивания"; f.steps[0] != want {
		t.Fatalf("started as %q, want %q", f.steps[0], want)
	}
}

// The operator's rule (T-134): a card with no repo is worked in the home
// directory. "~" is how the board spells it by hand, and YAML reads a bare ~
// as null, so the card arrives here with no repo at all either way.
func TestDispatchStartsACardWithNoRepoInTheHomeDirectory(t *testing.T) {
	for _, repo := range []string{"", "repo: ~", `repo: ""`, `repo: "~"`, "repo: ~/", "repo: $HOME", "repo: $HOME/"} {
		t.Run(repo, func(t *testing.T) {
			f := newWorker()
			d, card := dispatcher(t, f)
			if err := os.WriteFile(card, []byte(strings.Replace(workCard, "repo: work/proj", repo, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"}); err != nil {
				t.Fatal(err)
			}
			if want := "start:" + d.Home + ":T-042 Дотащить доску до перетаскивания"; f.steps[0] != want {
				t.Fatalf("started as %q, want %q", f.steps[0], want)
			}
		})
	}
}

func TestDispatchRefusesWithoutACard(t *testing.T) {
	f := newWorker()
	d, _ := dispatcher(t, f)
	if _, err := d.Dispatch(t.Context(), Work{Lang: "en"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
}

func TestDispatchRefusesOnAPanelThatStartsNoSessions(t *testing.T) {
	f := newWorker()
	d, card := dispatcher(t, f)
	d.Start = nil
	if _, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"}); !errors.Is(err, ErrCannotStart) {
		t.Fatalf("want ErrCannotStart, got %v", err)
	}
}

// A session that started and then could not be written into the card is the
// failure the operator has to be told about by name: the session is running,
// costing money, and nothing on the board points at it.
func TestAStartedSessionIsReportedEvenWhenTheCardWriteFails(t *testing.T) {
	f := newWorker()
	f.setErr = errors.New("the card moved on")
	d, card := dispatcher(t, f)

	res, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("a failed card write must not be reported as done")
	}
	if res.Session != "abc12345" {
		t.Fatalf("the started session must be named in the result, got %q", res.Session)
	}
	last := res.Steps[len(res.Steps)-1]
	if last.Error == "" || !strings.Contains(last.Error, "the card moved on") {
		t.Fatalf("the failing step must carry the reason: %+v", res.Steps)
	}
	for _, step := range f.steps {
		if strings.HasPrefix(step, "send:") {
			t.Fatal("the task was sent after the card write failed")
		}
	}
}

// Two dispatches at once would claim two numbers for one card and leave one
// session unnamed.
func TestASecondDispatchWhileOneRunsIsRefused(t *testing.T) {
	f := newWorker()
	d, card := dispatcher(t, f)
	inside, release := make(chan struct{}), make(chan struct{})
	d.SendFirst = func(context.Context, string, string) error {
		close(inside)
		<-release
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = d.Dispatch(context.Background(), Work{Card: card, Lang: "en"})
	}()
	<-inside
	if _, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("want ErrBusy, got %v", err)
	}
	close(release)
	<-done
}

// The session list shows no card, so the name has to say what the session is
// busy with: the card's number, then its title, cut to what a row can show.
func TestSessionNameIsTheCardsNumberAndTitle(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("ё", 100)
	for _, tc := range []struct{ id, title, want string }{
		{"T-068", "Форма карточки", "T-068 Форма карточки"},
		{"T-068", "", "T-068"},
		{"", "Без номера", words["en"].workerName},
		{"T-068", long, "T-068 " + strings.Repeat("ё", maxSessionNameRunes-len("T-068 ")-1) + "…"},
	} {
		if got := sessionName("en", tc.id, tc.title); got != tc.want {
			t.Errorf("sessionName(%q, %q) = %q, want %q", tc.id, tc.title, got, tc.want)
		}
	}
}

// A card write whose commit did not happen is a write that happened: the
// session field is in the file, so the card names the session, and stopping
// there leaves a running session with no task. The dispatch goes on, and the
// step says the commit is missing and why.
func TestADispatchWhoseCommitFailedStillHandsOverTheTask(t *testing.T) {
	f := newWorker()
	d, card := dispatcher(t, f)
	d.SetField = func(path, field, value string, expect *string) error {
		if err := f.set(path, field, value, expect); err != nil {
			return err
		}
		if err := board.SetField(path, field, value, expect); err != nil {
			return err
		}
		return fmt.Errorf("%w: git commit timed out", board.ErrWrittenNotCommitted)
	}

	res, err := d.Dispatch(t.Context(), Work{Card: card, Lang: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("a commit that did not happen must not stop the dispatch: %+v", res.Steps)
	}
	if last := f.steps[len(f.steps)-1]; last != "first:abc12345:"+Task("en", card) {
		t.Fatalf("the task was not delivered, steps: %q", f.steps)
	}
	raw, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"session: abc12345\n", "stage: active\n"} {
		if !strings.Contains(string(raw), line) {
			t.Fatalf("the card has no %q:\n%s", line, raw)
		}
	}
	for _, step := range res.Steps {
		if step.Name != "card" && step.Name != "stage" {
			continue
		}
		if !strings.Contains(step.Note, "not committed: git commit timed out") {
			t.Fatalf("step %s must say the commit is missing and why: %+v", step.Name, step)
		}
		if strings.Contains(step.Note+step.Error, "does not name it") {
			t.Fatalf("the card names the session, and step %s says it does not: %+v", step.Name, step)
		}
	}
}
