package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

var (
	// ErrCardTaken is a card whose session field already names one. Nothing was
	// done.
	ErrCardTaken = errors.New("the card already names a session")
	// ErrNoCard is a dispatch for a card that cannot be read. Nothing was done.
	ErrNoCard = errors.New("the card cannot be read")
	// ErrNoRepo is a card that names no repository a worker could start in.
	// Nothing was done.
	ErrNoRepo = errors.New("the card names no repository to work in")
)

// Work is one card handed to a session of its own: the card's path, and the
// language the session is spoken to in (see Lang), which is the page's.
type Work struct {
	Card string
	Lang string
}

// Dispatcher starts a session for a card and hands the card to it.
//
// The order it does that in is the whole of why this exists, and it is not
// free to change: the session is started with no prompt, its short id is
// written into the card, and the task is sent last. A session told to work a
// card before its own id is on it starts by writing the field itself, and from
// then on the panel's copy of the card and the agent's diverge — silently,
// because both writes succeed.
//
// Every reach outside is a function, as the Appointer's are, and for the same
// reason: which runtime is behind them is the fleet's business.
type Dispatcher struct {
	// Start starts a background session in cwd and under name, with no
	// prompt, and returns its short id. Nil is a panel that starts no
	// sessions.
	Start func(ctx context.Context, cwd, name string) (string, error)
	// List is this fleet's session list, and Send delivers one message into a
	// session as a turn of its own.
	List func(ctx context.Context) ([]daemon.Session, error)
	Send func(ctx context.Context, short, text string) error
	// SendFirst is what the Appointer's is: the send for the first message
	// into a session just started, on a fleet whose Send acknowledges a
	// message before the session can read it. Nil, and the task goes by Send.
	SendFirst func(ctx context.Context, short, text string) error
	// SetField writes one frontmatter field, refusing when the card no longer
	// holds the value the write was made against (board.SetField).
	SetField func(path, field, value string, expect *string) error
	// Home is the directory a card's repo field is read from: a worker starts
	// in Home/<repo>, the checkout its card names, and a card naming none that
	// exists is refused rather than started somewhere else (Workdir).
	Home string

	// Poll, StartWait and SendWait mean what the Appointer's do. Zero is the
	// default.
	Poll, StartWait, SendWait time.Duration

	busy sync.Mutex
}

// Dispatch hands one card to a session. An error means the request was refused
// and nothing was done; everything attempted is in the Result, in order: the
// session, the card's session field, its stage, the task. The first step that
// fails ends it, and a session already started is named in the Result whatever
// happened after — it is running, and nothing on the board points at it.
func (d *Dispatcher) Dispatch(ctx context.Context, w Work) (Result, error) {
	switch {
	case w.Card == "":
		return Result{}, fmt.Errorf("%w: a dispatch must name the card it hands over", ErrBadRequest)
	case d.Start == nil:
		return Result{}, ErrCannotStart
	}
	card, err := board.ParseCard(w.Card)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrNoCard, err)
	}
	if card.ParseError != "" {
		return Result{}, fmt.Errorf("%w: %s", ErrNoCard, card.ParseError)
	}
	if card.Session != "" {
		return Result{}, fmt.Errorf("%w: %s", ErrCardTaken, card.Session)
	}
	cwd, err := Workdir(d.Home, card.Repo)
	if err != nil {
		return Result{}, err
	}
	if !d.busy.TryLock() {
		return Result{}, ErrBusy
	}
	defer d.busy.Unlock()

	var res Result
	refuse := func(name string, err error) (Result, error) {
		res.Steps = append(res.Steps, Step{Name: name, Error: err.Error()})
		return res, nil
	}
	done := func(name, note string) {
		res.Steps = append(res.Steps, Step{Name: name, Note: note})
	}

	lang := Lang(w.Lang)
	short, err := d.Start(ctx, cwd, sessionName(lang, card.ID, card.Title))
	if err != nil {
		return refuse("session", err)
	}
	res.Session = short
	if err := waitListed(ctx, d.List, short, d.wait(d.StartWait, defaultStartWait), d.wait(d.Poll, defaultPoll)); err != nil {
		return refuse("session", err)
	}
	done("session", fmt.Sprintf("started %s in %s", short, cwd))

	// Both writes carry what the card held when it was read: between the read
	// and here the card may have been moved by a hand or by its own agent, and
	// writing over that is the thing the precondition exists to stop.
	empty := ""
	if err := d.SetField(w.Card, "session", short, &empty); err != nil {
		return refuse("card", fmt.Errorf("%s is running, and the card does not name it: %w", short, err))
	}
	done("card", "session -> "+short)

	// stage is written by the panel rather than left to the agent because the
	// card was moved into the column by a hand, and the board refuses a started
	// stage while the session field is empty — which is why this cannot come
	// first.
	if err := d.SetField(w.Card, "stage", "active", &card.Stage); err != nil {
		return refuse("stage", err)
	}
	done("stage", "stage -> active")

	// The refusal carries the task itself: the card names the session and the
	// session runs, so what is left for a hand to do is send this line into it.
	task := Task(lang, w.Card)
	if err := deliver(ctx, firstSend(d.Send, d.SendFirst), short, task, d.wait(d.StartWait, defaultStartWait), d.wait(d.Poll, defaultPoll)); err != nil {
		return refuse("task", fmt.Errorf("%w — %s is running and the card names it; send the task by hand: %s", err, short, task))
	}
	done("task", "delivered to "+short)
	res.OK = true
	return res, nil
}

// Workdir is the checkout a card's repo field names under home
// (board.NormalizeRepo). It must be a directory; anything else is ErrNoRepo
// with the reason.
func Workdir(home, repo string) (string, error) {
	if strings.TrimSpace(repo) == "" {
		return "", fmt.Errorf("%w: set repo on the card, a path from the home directory, e.g. src/fleetdeck", ErrNoRepo)
	}
	rel, err := board.NormalizeRepo(repo)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoRepo, err)
	}
	dir := filepath.Join(home, rel)
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return "", fmt.Errorf("%w: repo %s: %w", ErrNoRepo, rel, err)
	case !info.IsDir():
		return "", fmt.Errorf("%w: repo %s is not a directory: %s", ErrNoRepo, rel, dir)
	}
	return dir, nil
}

func (d *Dispatcher) wait(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return fallback
}

// Task is what a worker session is sent, in lang: one line naming its card,
// the same whichever card it is. One line for the reason Message is one — a
// multi-line reply can be left sitting in the prompt unsent.
func Task(lang, cardPath string) string {
	return fmt.Sprintf(words[Lang(lang)].task, "`"+cardPath+"`")
}

// maxSessionNameRunes bounds a session's name: a row of `claude agents` and of
// the panel's session list, not a place for the whole title.
const maxSessionNameRunes = 60

// sessionName is the name a card's session is started under: the card's number
// and title, because the session list shows no card and the name is what says
// what the session is busy with. A card with no number gets the worker's name,
// in lang.
func sessionName(lang, id, title string) string {
	if id == "" {
		return words[Lang(lang)].workerName
	}
	name := strings.TrimSpace(id + " " + title)
	if r := []rune(name); len(r) > maxSessionNameRunes {
		name = strings.TrimSpace(string(r[:maxSessionNameRunes-1])) + "…"
	}
	return name
}
