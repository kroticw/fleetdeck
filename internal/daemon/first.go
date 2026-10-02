package daemon

import (
	"context"
	"fmt"
	"time"
)

const (
	// firstWait bounds the whole of SendFirst: the boot, the reply and the
	// echo. Measured on CLI 2.1.278: a session started by `--bg` is listed the
	// moment the daemon claims a spare for it, its attach header says booting
	// for another ~0.6s, and the echo follows the reply within 0.3–1.5s. A
	// minute is the margin against a slow machine, not the expected time.
	firstWait = time.Minute
	firstPoll = 250 * time.Millisecond

	// freshCols and freshRows are the size a fresh background session runs at
	// (attach.go). An attach resizes, and the boot probe is an attach: at this
	// size it leaves the session as it found it.
	freshCols, freshRows = 200, 50
)

// ErrNotTaken is a first message a session did not take: it never finished
// booting, or the reply was acknowledged and its list record never echoed the
// text. Nothing went in twice, and nothing may be sent again blindly either —
// a text sitting unsubmitted in the prompt would then go in twice.
type ErrNotTaken struct {
	Session string
	Reason  string
}

func (e *ErrNotTaken) Error() string {
	return fmt.Sprintf("%s did not take its first message: %s", e.Session, e.Reason)
}

// SendFirst delivers the first message into a session that was started with no
// prompt, and reports it delivered only once the session shows it took it.
//
// SendText is not enough for that message. `reply` is acknowledged when the text
// is written into the session's terminal, and a session `--bg` has just
// reported is listed before its prompt reads anything: the text is echoed into
// a terminal in cooked mode and dropped when the prompt takes the terminal over
// (measured on CLI 2.1.278 — the raw bracketed paste stands above the banner on
// the session's screen, and its intent stays empty). The daemon says the boot
// is over in one place only, the attach header's booting flag, so that is
// polled first; and the list record's detail echoing the text is the one sign
// the prompt took it (docs/protocol/daemon-control-socket.md section 4), so
// that is polled after.
//
// A refusal of the reply itself comes back as the daemon's own error, so a
// caller keeps retrying a passing one; *ErrNotTaken is final.
func (c *Client) SendFirst(parent context.Context, session, text string) error {
	ctx, cancel := context.WithTimeout(parent, c.firstWait)
	defer cancel()

	booted := func() (bool, error) {
		a, err := c.Attach(ctx, session, freshCols, freshRows)
		if err != nil {
			return false, err
		}
		_ = a.Close()
		return !a.booting, nil
	}
	if err := c.pollFirst(parent, ctx, session, "it is still booting", booted); err != nil {
		return err
	}
	if err := c.SendText(ctx, session, text); err != nil {
		return err
	}
	taken := func() (bool, error) {
		sessions, err := c.ListSessions(ctx)
		if err != nil {
			return false, err
		}
		for _, s := range sessions {
			if s.Short == session {
				return s.Detail == text, nil
			}
		}
		return false, nil
	}
	return c.pollFirst(parent, ctx, session, "the reply was acknowledged, but its list record never showed the text taken", taken)
}

// pollFirst asks check until it answers yes or ctx, the wait's own, ends. The
// wait running out is *ErrNotTaken with reason; the caller's context ending is
// its error. The last error check returned is carried in the reason, so an
// attach the daemon refuses outright is not read as a boot that never ended.
func (c *Client) pollFirst(parent, ctx context.Context, session, reason string, check func() (bool, error)) error {
	var last error
	for {
		ok, err := check()
		if ok {
			return nil
		}
		if err != nil {
			last = err
		}
		select {
		case <-ctx.Done():
			if parent.Err() != nil {
				return parent.Err()
			}
			if last != nil {
				reason = fmt.Sprintf("%s (last answer: %v)", reason, last)
			}
			return &ErrNotTaken{Session: session, Reason: fmt.Sprintf("%s within %s", reason, c.firstWait)}
		case <-time.After(c.firstPoll):
		}
	}
}
