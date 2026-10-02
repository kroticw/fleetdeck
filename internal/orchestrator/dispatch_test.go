package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
)

const workCard = `---
id: T-042
zone: planned
stage: new
progress: 0
session: ""
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
	dir := t.TempDir()
	card := filepath.Join(dir, "T-042-card.md")
	if err := os.WriteFile(card, []byte(workCard), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Dispatcher{
		Start:     f.start,
		List:      f.list,
		Send:      f.send,
		SetField:  f.set,
		Workspace: dir,
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
		"start:" + filepath.Dir(card) + ":T-042",
		"set:T-042-card.md:session=abc12345:was=",
		"set:T-042-card.md:stage=active:was=new",
		"send:abc12345:" + Task("en", card),
	}
	if strings.Join(f.steps, "\n") != strings.Join(want, "\n") {
		t.Fatalf("steps:\n%s\nwant\n%s", strings.Join(f.steps, "\n"), strings.Join(want, "\n"))
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
	d.Send = func(context.Context, string, string) error {
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
