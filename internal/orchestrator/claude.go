package orchestrator

import (
	"context"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/executor"
)

// Claude is how a fleet reaches Claude Code: its daemon's sessions and the
// claude that starts and stops them. A nil function is unsupported, and the
// adapter leaves that operation nil too.
type Claude struct {
	// Start starts a background session in cwd under name, with no prompt,
	// and returns its short id.
	Start func(ctx context.Context, cwd, name string) (string, error)
	List  func(ctx context.Context) ([]daemon.Session, error)
	// Send delivers one message as a turn of its own; SendFirst is the send
	// for the first message into a session just started, on a fleet whose
	// Send acknowledges before the session can read it.
	Send      func(ctx context.Context, short, text string) error
	SendFirst func(ctx context.Context, short, text string) error
	Stop      func(ctx context.Context, short string) error
	Resume    func(short string) error
	Attach    func(ctx context.Context, short string, cols, rows int) (executor.Terminal, error)
	LastWords func(short string) (Said, error)

	// Poll and StartWait mean what the Appointer's do. Zero is the default.
	Poll, StartWait time.Duration
}

// ClaudeAdapter is Claude Code behind the executor contract. A session's key
// is its short id; its daemon's acknowledgement is what accepts a message.
func ClaudeAdapter(c Claude) executor.Adapter {
	wait := func(v, fallback time.Duration) time.Duration {
		if v > 0 {
			return v
		}
		return fallback
	}
	a := executor.Adapter{ID: executor.Claude}
	if c.Start != nil && c.List != nil {
		a.Start = func(ctx context.Context, req executor.StartRequest) (executor.Session, error) {
			short, err := c.Start(ctx, req.CWD, req.Name)
			if err != nil {
				return executor.Session{}, err
			}
			s := executor.Session{Key: executor.Key(short), Executor: executor.Claude, Name: req.Name, CWD: req.CWD, Lifecycle: "live"}
			return s, waitListed(ctx, c.List, short, wait(c.StartWait, defaultStartWait), wait(c.Poll, defaultPoll))
		}
	}
	if c.List != nil {
		a.List = func(ctx context.Context) ([]executor.Session, error) {
			listed, err := c.List(ctx)
			sessions := make([]executor.Session, 0, len(listed))
			for _, s := range listed {
				sessions = append(sessions, claudeSession(s))
			}
			return sessions, err
		}
	}
	if c.Send != nil {
		a.Send = func(ctx context.Context, key executor.Key, m executor.Message) (executor.Receipt, error) {
			var err error
			if m.First {
				err = deliver(ctx, firstSend(c.Send, c.SendFirst), string(key), m.Text, wait(c.StartWait, defaultStartWait), wait(c.Poll, defaultPoll))
			} else {
				err = c.Send(ctx, string(key), m.Text)
			}
			if err != nil {
				return executor.Receipt{OperationID: m.OperationID, State: executor.Failed}, err
			}
			return executor.Receipt{OperationID: m.OperationID, State: executor.Accepted}, nil
		}
	}
	if c.Stop != nil {
		a.Stop = func(ctx context.Context, key executor.Key) error { return c.Stop(ctx, string(key)) }
	}
	if c.Resume != nil {
		a.Resume = func(_ context.Context, key executor.Key) (executor.Session, error) {
			return executor.Session{Key: key, Executor: executor.Claude}, c.Resume(string(key))
		}
	}
	if c.Attach != nil {
		a.OpenTerminal = func(ctx context.Context, key executor.Key, cols, rows int) (executor.Terminal, error) {
			return c.Attach(ctx, string(key), cols, rows)
		}
	}
	if c.LastWords != nil {
		a.LastWords = func(_ context.Context, key executor.Key) (executor.Said, error) {
			said, err := c.LastWords(string(key))
			return executor.Said{Words: said.Words, Transcript: said.Transcript, Name: said.Name}, err
		}
	}
	return a
}

func claudeSession(s daemon.Session) executor.Session {
	out := executor.Session{Key: executor.Key(s.Short), Executor: executor.Claude, NativeID: s.SessionID, Name: s.Name, CWD: s.CWD, PID: s.PID, Lifecycle: "live", Activity: s.State}
	switch s.Waiting() {
	case daemon.Yes:
		out.NeedsHuman = executor.Yes
	case daemon.No:
		out.NeedsHuman = executor.No
	}
	if s.Needs != nil {
		out.Reason = *s.Needs
	}
	return out
}
