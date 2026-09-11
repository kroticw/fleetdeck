package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
)

var (
	// ErrBadRequest is a request that names neither path, or both, or a
	// session by something that is not a short id. Nothing was done.
	ErrBadRequest = errors.New("appoint either a new session or one existing session by its short id")
	// ErrCannotStart is the new-session path on a panel that has no claude to
	// start sessions with. Nothing was done.
	ErrCannotStart = errors.New("this panel does not start sessions")
	// ErrNoBoard is a panel with no board: an orchestrator would have nothing to
	// keep, and the brief nowhere to go. Nothing was done.
	ErrNoBoard = errors.New("this panel has no board")
	// ErrBusy is a second appointment while one is running. Nothing was done.
	ErrBusy = errors.New("an orchestrator is being appointed already")
)

// shortID is the daemon's short session id.
var shortID = regexp.MustCompile(`^[0-9a-f]{8}$`)

const (
	defaultPoll      = 250 * time.Millisecond
	defaultStartWait = time.Minute
	defaultSendWait  = 5 * time.Second
)

// Request is one appointment: a new session, or the existing one Session names.
type Request struct {
	New     bool
	Session string
	// Lang is the language of the brief and the message; see Lang.
	Lang string
}

// Step is one thing an appointment did, or — Error set — refused to do and why.
type Step struct {
	Name  string `json:"name"`
	Note  string `json:"note,omitempty"`
	Error string `json:"error,omitempty"`
}

// Result is what an appointment came to. Session is the session it was about,
// also when a started one got no further; OK is every step done.
type Result struct {
	Steps   []Step `json:"steps"`
	Session string `json:"session,omitempty"`
	OK      bool   `json:"ok"`
}

// Appointer appoints orchestrators. Every reach outside is a function, so a
// test — and a stand — can stand in for the daemon, for claude and for the
// configuration.
type Appointer struct {
	Paths Paths
	// List is the daemon's session list.
	List func(ctx context.Context) ([]daemon.Session, error)
	// Send delivers one message into a session, as the daemon's reply does.
	Send func(ctx context.Context, short, text string) error
	// Start starts a background session in cwd under name and returns its
	// short id. Nil is a panel that does not start sessions.
	Start func(ctx context.Context, cwd, name string) (string, error)
	// Pin makes short the panel's orchestrator: configuration and column.
	Pin func(short string) error

	// Poll is the pause between two asks of the daemon. StartWait bounds the
	// wait for a started session to be listed and take its message; SendWait
	// bounds how long an existing session may refuse a message. Zero is the
	// default.
	Poll, StartWait, SendWait time.Duration

	busy sync.Mutex
}

// Appoint runs one appointment. An error means the request was refused and
// nothing was done; everything that was attempted is in the Result, in order:
// the brief, the session when a new one is started, the message, the pin. The
// first step that fails ends it.
func (a *Appointer) Appoint(ctx context.Context, req Request) (Result, error) {
	switch {
	case req.New == (req.Session != ""):
		return Result{}, ErrBadRequest
	case !req.New && !shortID.MatchString(req.Session):
		return Result{}, ErrBadRequest
	case req.New && a.Start == nil:
		return Result{}, ErrCannotStart
	case a.Paths.Board == "":
		return Result{}, ErrNoBoard
	}
	if !a.busy.TryLock() {
		return Result{}, ErrBusy
	}
	defer a.busy.Unlock()

	lang := Lang(req.Lang)
	res := Result{Session: req.Session}
	refuse := func(name string, err error) (Result, error) {
		res.Steps = append(res.Steps, Step{Name: name, Error: err.Error()})
		return res, nil
	}
	done := func(name, note string) {
		res.Steps = append(res.Steps, Step{Name: name, Note: note})
	}

	// An existing session is looked up before anything is written: a brief
	// written for a session that is not there points at nothing.
	if !req.New {
		if err := a.listed(ctx, req.Session); err != nil {
			return refuse("session", err)
		}
	}

	path := BriefPath(a.Paths)
	brief, err := Brief(lang, a.Paths)
	if err == nil {
		err = WriteBrief(path, brief)
	}
	if err != nil {
		return refuse("brief", err)
	}
	done("brief", "wrote "+path)

	sendWait := a.wait(a.SendWait, defaultSendWait)
	if req.New {
		cwd := filepath.Dir(a.Paths.Board)
		short, err := a.Start(ctx, cwd, words[lang].sessionName)
		if err != nil {
			return refuse("session", err)
		}
		res.Session = short
		if err := a.appear(ctx, short); err != nil {
			return refuse("session", err)
		}
		done("session", fmt.Sprintf("started %s in %s", short, cwd))
		// A session that has just come up can take a moment more to take input.
		sendWait = a.wait(a.StartWait, defaultStartWait)
	}

	if err := a.deliver(ctx, res.Session, Message(lang, path), sendWait); err != nil {
		return refuse("message", err)
	}
	done("message", "delivered to "+res.Session)

	if err := a.Pin(res.Session); err != nil {
		return refuse("pin", fmt.Errorf("the session has its message, but it is not pinned as the orchestrator: %w", err))
	}
	done("pin", "orchestrator.session -> "+res.Session)
	res.OK = true
	return res, nil
}

// Preview is what an appointment will do, for the wizard to show before the
// person chooses: where the brief goes and what it says, the one line the
// session is sent, and where and under which name a new session starts.
type Preview struct {
	Path      string `json:"path"`
	Message   string `json:"message"`
	Brief     string `json:"brief"`
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
	// CanStart is false on a panel that does not start sessions.
	CanStart bool `json:"canStart"`
}

// Preview builds, in lang, what Appoint would write and send. It writes
// nothing.
func (a *Appointer) Preview(lang string) (Preview, error) {
	lang = Lang(lang)
	brief, err := Brief(lang, a.Paths)
	if err != nil {
		return Preview{}, err
	}
	path := BriefPath(a.Paths)
	return Preview{
		Path:      path,
		Message:   Message(lang, path),
		Brief:     string(brief),
		Workspace: filepath.Dir(a.Paths.Board),
		Name:      words[lang].sessionName,
		CanStart:  a.Start != nil,
	}, nil
}

func (a *Appointer) wait(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

func (a *Appointer) pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(a.wait(a.Poll, defaultPoll)):
		return nil
	}
}

// listed checks that the daemon lists short as a live session.
func (a *Appointer) listed(ctx context.Context, short string) error {
	sessions, err := a.List(ctx)
	if err != nil {
		return fmt.Errorf("the daemon's session list is not available: %w", err)
	}
	if !alive(sessions, short) {
		return fmt.Errorf("the daemon does not list %s as a running session", short)
	}
	return nil
}

// appear waits for a session that was just started to be listed.
func (a *Appointer) appear(ctx context.Context, short string) error {
	limit := a.wait(a.StartWait, defaultStartWait)
	deadline := time.Now().Add(limit)
	for {
		sessions, err := a.List(ctx)
		if err == nil && alive(sessions, short) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("started %s, but the daemon did not list it within %s: find it with `claude agents`, and choose it here once it is running", short, limit)
		}
		if err := a.pause(ctx); err != nil {
			return err
		}
	}
}

// deliver sends text into short, asking again while the session is coming up
// or momentarily not taking input, and not past wait.
func (a *Appointer) deliver(ctx context.Context, short, text string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := a.Send(ctx, short, text)
		if err == nil || !passing(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not take the message within %s (%w): it is most likely asking something on its own screen — answer it there, then run the wizard again", short, wait, err)
		}
		if err := a.pause(ctx); err != nil {
			return err
		}
	}
}

// passing is a refusal that goes away by itself: the session is starting,
// respawning, or not taking input this moment.
func passing(err error) bool {
	var starting *daemon.ErrStarting
	var noreply *daemon.ErrNoreply
	var respawning *daemon.ErrRespawning
	return errors.As(err, &starting) || errors.As(err, &noreply) || errors.As(err, &respawning)
}

func alive(sessions []daemon.Session, short string) bool {
	for _, s := range sessions {
		if s.Short == short {
			return !s.Dying
		}
	}
	return false
}
