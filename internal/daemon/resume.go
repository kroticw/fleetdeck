package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Resuming a stopped session: the one operation this client performs that
// starts a process rather than reading one or typing into one.
//
// It is `dispatch`, and docs/protocol/daemon-control-socket.md describes it in
// a section of its own. Two things about it shape everything below.
//
// The reply says nothing about the session. `{"ok": true}` means the daemon
// accepted the descriptor, not that a worker came up — the worker is started
// after the reply, and every way it can fail to start happens past it. So the
// answer to "did the resume work" is not in the reply at all: it is in the
// session list afterwards, which is why Resume does not return until it has
// watched one.
//
// And a resumed worker that dies is terminal, not a state to wait through. The
// daemon does not respawn one that died because its working directory was gone
// or its transcript was missing, so the first crashed reading is the answer and
// waiting out the rest of the timeout only delays it. This is the one place
// where this client deliberately behaves differently from a plain
// `claude --bg --resume`, whose worker really can flicker through a crash and
// recover.
const (
	// resumeTimeout bounds the whole wait. A session with a long history
	// spends real time replaying it before it answers, and half a minute is
	// what claude-agents-mcp's own resume — measured against this daemon,
	// against real sessions — allows for it.
	resumeTimeout = 30 * time.Second

	// resumeSettle is how long the session must go on reading as usable
	// before the resume is called done. A worker moves through several
	// states as it comes up, and a single usable reading taken between two
	// of them would have the panel announce a session that is not answering
	// yet.
	resumeSettle = 3 * time.Second

	// resumePoll is how often the list is asked. Short enough that a crash
	// is reported promptly, and it runs only while somebody is watching a
	// button they just pressed — never on the panel's own cadence.
	resumePoll = 300 * time.Millisecond

	// resumeRetryDelay is the pause before the one retry a refused dispatch
	// gets. The refusal it exists for is transient — a daemon mid-restart,
	// a record being rewritten under it — and nothing was started, so the
	// retry cannot double-resume anything.
	resumeRetryDelay = 1500 * time.Millisecond

	// resumeDispatchTimeoutMS is the daemon's own budget for the dispatch,
	// carried in the request. It bounds the daemon's work, not this client's
	// wait for it.
	resumeDispatchTimeoutMS = 5000

	// resumeDispatchAttempts is dispatch plus one retry. Deliberately not
	// more: past a second refusal the daemon is not having a bad moment, it
	// is saying no, and a third attempt only makes the operator wait longer
	// to hear it.
	resumeDispatchAttempts = 2
)

// ResumeSpec is everything the daemon needs to bring one session back.
//
// It is filled from Claude Code's job store (internal/jobs), and nothing in it
// is read off the socket: the daemon's list has no record of a session it is
// no longer running, which is the whole reason the store is read at all.
//
// None of this reaches a browser. Flags in particular can carry a --settings
// blob, and the panel's resume route takes a short id and nothing else.
type ResumeSpec struct {
	// Short is the id the session is addressed by, and the one it comes back
	// under.
	Short string

	// SessionID is the id the session is known by; ResumeID is the id it is
	// resumed by. They are the same on an ordinary session and differ on one
	// that was itself resumed from another, and both are UUIDs — so putting
	// them the wrong way round resumes a different conversation without
	// anything downstream objecting.
	SessionID string
	ResumeID  string

	// CWD is the directory the session ran in. Empty is legal: the daemon
	// falls back to a default, which is why internal/jobs treats an empty
	// working directory as present rather than missing.
	CWD string

	// Name and Intent are what the session was called and what it was last
	// asked to do. They are seeded back so the resumed session is listed as
	// itself rather than as a nameless new entry.
	Name   string
	Intent string

	// Flags is the command line the session was started with, carried back
	// verbatim: its model, its permission mode, its settings. Dropping them
	// resumes the history on a different session.
	Flags []string

	// TranscriptPath is where the session's transcript actually is, when the
	// caller found it. Empty means it was not found, and the field is then
	// left out of the request entirely rather than sent empty — the daemon
	// reads the key's presence, and an empty path points the worker at
	// nothing instead of letting it look the transcript up itself.
	TranscriptPath string
}

// ErrResumeNoID means the session carries no id to resume by. Refused here
// rather than dispatched: the daemon would refuse it too, but in words about
// a descriptor rather than about a session.
var ErrResumeNoID = errors.New("session has no id to resume by")

// Resume brings a stopped session back in place — same short id, same session,
// its own history — and returns only once the worker is verified live, or once
// it is certain it is not coming up.
//
// A nil return means a session that was answering the daemon at the moment
// this returned. Every error carries the daemon's own words where the daemon
// supplied any, because this failure lands in front of an operator who has to
// decide what to do about it, and "resume failed" supports no decision at all.
//
// It does not clean up after a worker that came up and crashed. The daemon's
// own removal of a session is not part of this protocol — claude-agents-mcp
// shells out to `claude stop` for it, which this panel does not do (see
// internal/jobs' package doc for why it shells out to nothing). A crashed
// worker is therefore left where the daemon put it, visible in the list as
// what it is, and the error says so.
func (c *Client) Resume(ctx context.Context, spec ResumeSpec) error {
	if spec.ResumeID == "" {
		return fmt.Errorf("%w: %s", ErrResumeNoID, spec.Short)
	}

	var lastErr error
	for attempt := 0; attempt < resumeDispatchAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.resumeRetryDelay):
			}
		}

		if err := c.dispatchResume(ctx, spec); err != nil {
			// Nothing was started, so there is nothing to wait for and
			// nothing to tidy — the next attempt starts clean. A missing
			// control key is not a bad moment the daemon is having, so it
			// is not retried.
			lastErr = err
			if errors.Is(err, ErrNoControlKey) {
				return err
			}
			continue
		}

		err := c.awaitResumed(ctx, spec.Short)
		if err == nil {
			return nil
		}
		// A worker did start. Dispatching a second one on top of it is how
		// one failed resume becomes two sessions, so this is the end.
		return err
	}
	return lastErr
}

// dispatchResume sends the descriptor and reports whether the daemon took it.
// It says nothing about the worker; see Resume's own comment.
func (c *Client) dispatchResume(ctx context.Context, spec ResumeSpec) error {
	err := c.dispatchResumeOnce(ctx, spec)
	if isProtoErr(err) {
		// Safe for the same reason SendText's retry is: the whole request
		// goes out in one write, and the daemon checks proto before it
		// executes the operation, so an EPROTO answer means no worker was
		// ever started.
		c.invalidateProto()
		err = c.dispatchResumeOnce(ctx, spec)
	}
	return err
}

func (c *Client) dispatchResumeOnce(ctx context.Context, spec ResumeSpec) error {
	key, err := c.keyFunc()
	if err != nil {
		return wrapNoControlKey(err)
	}

	proto, err := c.ensureProto(ctx)
	if err != nil {
		return err
	}

	nonce, err := resumeNonce()
	if err != nil {
		return err
	}

	launch := map[string]interface{}{
		"mode":      "resume",
		"sessionId": spec.ResumeID,
		// In place, never a copy: a fork would leave the stopped session
		// where it is and start a second one beside it, which is the outcome
		// the whole operation exists to avoid.
		"fork":     false,
		"flagArgs": flagsOrEmpty(spec.Flags),
	}
	if spec.TranscriptPath != "" {
		launch["transcriptPath"] = spec.TranscriptPath
	}

	descriptor := map[string]interface{}{
		"proto":        proto,
		"short":        spec.Short,
		"nonce":        nonce,
		"sessionId":    spec.SessionID,
		"createdAt":    time.Now().UnixMilli(),
		"source":       "fleet",
		"cwd":          spec.CWD,
		"launch":       launch,
		"env":          map[string]interface{}{},
		"isolation":    "none",
		"respawnFlags": flagsOrEmpty(spec.Flags),
		"seed": map[string]interface{}{
			"intent": spec.Intent,
			"name":   spec.Name,
		},
	}

	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	c.setDeadline(ctx, conn)

	req := map[string]interface{}{
		"proto":     proto,
		"op":        "dispatch",
		"d":         descriptor,
		"timeoutMs": resumeDispatchTimeoutMS,
		"auth":      key,
	}
	if err := c.writeRequest(conn, req); err != nil {
		return err
	}

	resp, err := readResponse(conn)
	if err != nil {
		return err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		return refusedResume(resp)
	}
	return nil
}

// refusedResume reads a refusal the way this one operation needs it read.
//
// daemonError maps the reply's `code` onto a typed error and deliberately
// ignores its `error` text, which is right everywhere it is used: those codes
// are a closed vocabulary and the text adds nothing to them. A refused
// dispatch is the exception. It is the only refusal in this client that lands
// in front of an operator as the answer to a button they just pressed, and the
// daemon's own sentence is the whole of what distinguishes "the daemon is
// restarting, try again" from "this session is already running".
//
// The typed error is kept underneath, so the EPROTO retry above still
// recognises its own case through the wrapping.
func refusedResume(resp map[string]interface{}) error {
	typed := daemonError(resp)
	text, _ := resp["error"].(string)
	if text = strings.TrimSpace(text); text == "" {
		return typed
	}
	return fmt.Errorf("the daemon refused the resume: %s (%w)", text, typed)
}

// awaitResumed watches the session list until the resumed worker is either
// live or certainly not coming.
//
// Four outcomes, and the two terminal ones are what keep this from being a
// thirty-second wait on every failure:
//
//   - it reads as usable for the whole settle window: resumed;
//   - it reads as crashed: over, with the roster's reason;
//   - it was in the list and then left it: over, it was retired;
//   - the deadline passes: over, saying which of "never appeared" and "never
//     settled" happened, because those call for different things from a
//     person.
func (c *Client) awaitResumed(ctx context.Context, short string) error {
	deadline := time.Now().Add(c.resumeTimeout)
	var seen bool
	var lastState string
	var usableSince time.Time

	for {
		sessions, err := c.ListSessions(ctx)
		if err != nil {
			// The list failing is not the worker failing: the daemon may be
			// busy starting it. Keep asking until the deadline, and report
			// the read failure only if nothing better ever arrives.
			if time.Now().After(deadline) {
				return fmt.Errorf("resumed worker %s could not be checked on: %w", short, err)
			}
		} else {
			session, found := findSession(sessions, short)
			switch {
			case !found && seen:
				return fmt.Errorf("resumed worker %s exited during startup%s", short, reasonSuffix(lastState))
			case !found:
				// Not there yet. The daemon accepted the dispatch, so it is
				// still on its way until the deadline says otherwise.
			case resumeCrashed(session):
				return fmt.Errorf("resumed worker %s crashed during startup%s", short, reasonSuffix(session.Detail))
			default:
				seen = true
				lastState = session.State
				if resumeUsable(session) {
					if usableSince.IsZero() {
						usableSince = time.Now()
					}
					if time.Since(usableSince) >= c.resumeSettle {
						return nil
					}
				} else {
					// Still coming up. The window starts again when it next
					// reads usable, so a worker that flickers back out of a
					// usable state does not carry a half-finished window
					// with it.
					usableSince = time.Time{}
				}
			}
		}

		if time.Now().After(deadline) {
			if !seen {
				return fmt.Errorf("resumed worker %s never registered with the daemon within %s", short, c.resumeTimeout)
			}
			return fmt.Errorf("resumed worker %s did not settle within %s (last state %q)", short, c.resumeTimeout, lastState)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.resumePoll):
		}
	}
}

func findSession(sessions []Session, short string) (Session, bool) {
	for _, s := range sessions {
		if s.Short == short {
			return s, true
		}
	}
	return Session{}, false
}

// resumeCrashed and resumeUsable read the daemon's own state vocabulary, which
// is not the vocabulary Waiting and Stalled read: those two are about a running
// session needing a person, and these two are about a worker having come up at
// all. They are deliberately separate for that reason — a rule that answered
// both questions would have to change for either.
func resumeCrashed(s Session) bool { return s.State == "crashed" }

// resumeUsable is "it is running and it is past its startup": any state the
// daemon reports other than the empty one (nothing recorded yet), "resuming"
// (still replaying its history) and "crashed".
func resumeUsable(s Session) bool {
	return s.State != "" && s.State != "resuming" && !resumeCrashed(s) && !s.Dying
}

// reasonSuffix appends the daemon's own words when it supplied any. An empty
// reason yields nothing at all rather than a dangling colon.
func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// flagsOrEmpty never sends JSON null where the daemon expects a list. A nil Go
// slice marshals to null, and a descriptor whose flagArgs is null is not the
// same request as one whose flagArgs is empty.
func flagsOrEmpty(flags []string) []string {
	if flags == nil {
		return []string{}
	}
	return flags
}

// resumeNonce is the descriptor's own one-off id, eight hex characters, the
// shape the daemon's other clients use.
func resumeNonce() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate resume nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
