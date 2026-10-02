package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
)

// listWait bounds asking the fleet whether the session is still there. It is
// one question with one answer, and a runtime that does not answer it must not
// hold up a cleanup the operator is watching.
const listWait = 5 * time.Second

// Accepted is one card the operator has accepted, and the session that worked
// it.
type Accepted struct {
	Session string
	Card    string
}

// Said is what a session leaves behind when it is put out: the last thing it
// said, where the rest of what it said stays, and the name it was known by. All
// three go into the archive entry, and they are read together because finding a
// session's transcript is the expensive part of reading any of them.
type Said struct {
	Words      string
	Transcript string
	Name       string
}

// Cleaner tidies away the session behind an accepted card: the pin, the
// knowledge, the entry in the board's archive, and the session itself.
//
// The order is the whole of why this exists, and it is not free to change. A
// session put out is a session nobody can read any more: `claude stop` keeps it
// resumable, but nothing resumes a session whose card was closed weeks ago, and
// in practice the transcript is all that is left. So the knowledge comes off it
// first and the archive entry is written before anything is put out — and a
// failure at either of those two steps ends the cleanup with the session still
// running, which is the one outcome that loses nothing.
//
// A pin left behind is not that kind of failure: it costs a line at the top of
// a list, so it is reported and the sequence carries on.
//
// Every reach outside is a function, as the Appointer's and the Dispatcher's
// are, and for the same reason: which runtime is behind them is the fleet's
// business and never this package's.
type Cleaner struct {
	// Unpin drops the session from the pin set it was put in when the work
	// started.
	Unpin func(short string) error

	// Words is what the session leaves behind, taken off its transcript. It is
	// what the archive entry carries, and its failure is what stops the
	// cleanup.
	Words func(short string) (Said, error)

	// Archive records the session in the board's archive, with what Words read.
	Archive func(a Accepted, said Said) error

	// Stop puts the session out gracefully — it stays resumable — and List is
	// the fleet's session list, asked whether there is anything to put out at
	// all.
	Stop func(ctx context.Context, short string) error
	List func(ctx context.Context) ([]daemon.Session, error)
}

// Cleanup tidies away a. An error means the request was refused and nothing was
// done; everything attempted is in the Result, in order, and OK is every step
// done. A step that failed leaves its own words in the Result: they are what
// the operator acts on, and a cleanup that stopped halfway must say where.
func (c *Cleaner) Cleanup(ctx context.Context, a Accepted) (Result, error) {
	switch {
	case a.Session == "":
		return Result{}, fmt.Errorf("%w: a cleanup must name the session it tidies away", ErrBadRequest)
	case a.Card == "":
		return Result{}, fmt.Errorf("%w: a cleanup must name the card that was accepted", ErrBadRequest)
	case c.Unpin == nil || c.Words == nil || c.Archive == nil || c.Stop == nil || c.List == nil:
		return Result{}, fmt.Errorf("%w: this panel is not wired to tidying a session away", ErrCannotStart)
	}

	res := Result{Session: a.Session}
	refuse := func(name string, err error) {
		res.Steps = append(res.Steps, Step{Name: name, Error: err.Error()})
	}
	done := func(name, note string) {
		res.Steps = append(res.Steps, Step{Name: name, Note: note})
	}

	if err := c.Unpin(a.Session); err != nil {
		refuse("pin", fmt.Errorf("%s stays pinned: %w", a.Session, err))
	} else {
		done("pin", a.Session+" is no longer pinned")
	}

	said, err := c.Words(a.Session)
	if err != nil {
		refuse("words", fmt.Errorf("%w — %s is left running, so nothing it knows is lost", err, a.Session))
		return res, nil
	}
	done("words", fmt.Sprintf("read %d characters of what %s said", len([]rune(said.Words)), a.Session))

	if err := c.Archive(a, said); err != nil {
		refuse("archive", fmt.Errorf("%w — %s is left running, so nothing it knows is lost", err, a.Session))
		return res, nil
	}
	done("archive", "recorded in the board's archive")

	// Every step above is done, so anything from here leaves the knowledge
	// kept: what is left to report is only whether the session is out.
	if !c.listed(ctx, a.Session) {
		refuse("session", fmt.Errorf("there was nothing to put out: this fleet does not list %s as running", a.Session))
		return res, nil
	}
	if err := c.Stop(ctx, a.Session); err != nil {
		refuse("session", fmt.Errorf("%s is still running: %w", a.Session, err))
		return res, nil
	}
	done("session", a.Session+" is out, and can be brought back with its history")
	// Read off the steps rather than set here: a pin that stayed behind does
	// not stop the sequence, and a cleanup that reported OK with a failed step
	// in it would be a panel contradicting its own report.
	res.OK = clean(res.Steps)
	return res, nil
}

func clean(steps []Step) bool {
	for _, s := range steps {
		if s.Error != "" {
			return false
		}
	}
	return true
}

// listed reports whether the fleet still runs this session. A list that cannot
// be read answers yes: it says nothing about the session, and reading silence
// as "already gone" would leave a live session running under an accepted card —
// the very thing the operator asked to be tidied away. The stop then answers
// for itself, in the runtime's own words.
func (c *Cleaner) listed(ctx context.Context, short string) bool {
	lctx, cancel := context.WithTimeout(ctx, listWait)
	defer cancel()
	sessions, err := c.List(lctx)
	if err != nil {
		return true
	}
	return alive(sessions, short)
}
