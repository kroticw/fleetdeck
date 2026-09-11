package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/daemon"
)

// Terminal is one held, two-way view of a session's terminal: what
// daemon.Attachment provides. It is an interface here only so the bridge below can
// be tested without a daemon.
type Terminal interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(ctx context.Context, cols, rows int) error
	Writable() bool
	Close() error
}

// The bounds a geometry is checked against, at connect and on every resize. The
// daemon's own ceiling was not established by measurement; these are simply larger
// than any window a person has, so anything beyond them is a broken or hostile client
// rather than a big screen. The value reaches a PTY other people are watching.
const (
	maxTerminalCols = 500
	maxTerminalRows = 200
)

// ptyOpenTimeout bounds attaching to the daemon. Past that, the stream has no
// deadline of its own: it ends when the browser leaves, the session exits, or a
// kick arrives.
const ptyOpenTimeout = 10 * time.Second

// Close codes the browser can tell apart. The 4000 range is the one RFC 6455
// leaves to applications; the reason text goes alongside in the close frame.
const (
	statusSessionEnded      websocket.StatusCode = 4000 // the session exited, or the daemon closed the stream
	statusKicked            websocket.StatusCode = 4001 // another attacher evicted this one; reason carries the daemon's words
	statusBadControl        websocket.StatusCode = 4400 // a text frame the bridge does not accept
	statusKeyRefused        websocket.StatusCode = 4401 // the daemon refused the control key
	statusNoSuchSession     websocket.StatusCode = 4404 // no session by that id
	statusDaemonUnavailable websocket.StatusCode = 4503 // no daemon to attach through
)

// maxCloseReason is what fits in a close frame's reason: 125 bytes of payload,
// two of them taken by the status code.
const maxCloseReason = 123

// handlePTY bridges a browser WebSocket to a held attach on one session's terminal.
//
// Frames, both ways:
//
//   - daemon to browser, binary: the session's terminal bytes, as they arrive;
//   - daemon to browser, text: {"type":"ready","writable":bool} once on connect, and
//     {"type":"error","error":"..."} when something the browser asked for was refused;
//   - browser to daemon, binary: keystrokes, written into the session unchanged;
//   - browser to daemon, text: {"type":"resize","cols":N,"rows":M}.
//
// The geometry comes in the query string (?cols=&rows=) and is checked before
// anything else happens, because attaching at it resizes the session for everyone
// watching it. After that the geometry only changes by a resize message, which the
// daemon applies to this attacher alone and without reconnecting — so the socket
// opens exactly one attach for its whole life, and a key press never sends a
// geometry the way POST .../keys does.
//
// Unlike /ws, this socket writes into a live session. The browser sends no preflight
// for a WebSocket handshake, so the JSON content-type half of guard does not reach
// it: terminalAllowed is the whole defence, and is kept in one named place so a
// second line can be added there without touching the rest of the route.
func (d Deps) handlePTY(w http.ResponseWriter, r *http.Request) {
	if d.Attach == nil {
		unavailable(w, "a daemon")
		return
	}
	if !terminalAllowed(r) {
		refuseForeignOrigin(w)
		return
	}
	cols, rows, err := terminalGeometry(r.URL.Query())
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// terminalAllowed has already made the origin decision; the library's own
		// check is off so there is one rule, not two that can drift.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	// A paste arrives as one message; this is the same ceiling an HTTP body has.
	conn.SetReadLimit(maxBodyBytes)

	// Attaching happens after the upgrade, not before: a refused handshake reaches a
	// page's script as a bare 1006 with no reason at all, while a close frame carries
	// a code and a reason the panel can show.
	openCtx, cancelOpen := context.WithTimeout(r.Context(), ptyOpenTimeout)
	term, err := d.Attach(openCtx, r.PathValue("id"), cols, rows)
	cancelOpen()
	if err != nil {
		closeForAttachError(conn, err)
		return
	}
	defer func() { _ = term.Close() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	if err := writeControl(ctx, conn, map[string]any{"type": "ready", "writable": term.Writable()}); err != nil {
		return
	}

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		defer cancel()
		pumpToBrowser(ctx, conn, term)
	}()
	pumpToSession(ctx, conn, term)

	// Whichever side ended first, the other has to stop too. Closing the attach is
	// what unblocks a Read waiting on a quiet session.
	cancel()
	_ = term.Close()
	<-streamDone
}

// terminalAllowed decides whether a request may open a terminal socket. Today that
// is the origin rule every route shares; a second, independent check belongs here.
func terminalAllowed(r *http.Request) bool {
	return originAllowed(r)
}

func terminalGeometry(q url.Values) (cols, rows int, err error) {
	cols, err = boundedInt(q.Get("cols"), "cols", maxTerminalCols)
	if err != nil {
		return 0, 0, err
	}
	rows, err = boundedInt(q.Get("rows"), "rows", maxTerminalRows)
	if err != nil {
		return 0, 0, err
	}
	return cols, rows, nil
}

func boundedInt(s, name string, maxValue int) (int, error) {
	if s == "" {
		return 0, errors.New(name + " is required")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxValue {
		return 0, errors.New(name + " must be a whole number from 1 to " + strconv.Itoa(maxValue))
	}
	return n, nil
}

// pumpToBrowser copies the session's bytes into binary frames until the stream ends,
// then closes the socket with a code that says how it ended.
func pumpToBrowser(ctx context.Context, conn *websocket.Conn, term Terminal) {
	buf := make([]byte, 32*1024)
	for {
		n, err := term.Read(buf)
		if n > 0 {
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
			cancel()
			if werr != nil {
				return
			}
		}
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			// The browser left and the attach was closed on its way out.
			return
		}
		var kicked *daemon.ErrKicked
		switch {
		case errors.As(err, &kicked):
			closeWith(conn, statusKicked, "kicked: "+kicked.Detail)
		case errors.Is(err, io.EOF):
			closeWith(conn, statusSessionEnded, "session ended")
		default:
			closeWith(conn, websocket.StatusInternalError, "terminal stream failed")
		}
		return
	}
}

// pumpToSession reads the browser's frames until it leaves or sends something the
// bridge does not accept.
func pumpToSession(ctx context.Context, conn *websocket.Conn, term Terminal) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if !term.Writable() {
				// Refused where the operator sees it, and the terminal stays open:
				// reading a session needs no key.
				if writeControl(ctx, conn, map[string]any{"type": "error", "error": "this terminal cannot type: the control key is unavailable"}) != nil {
					return
				}
				continue
			}
			if _, err := term.Write(data); err != nil {
				_ = writeControl(ctx, conn, map[string]any{"type": "error", "error": "keys not delivered: " + err.Error()})
				return
			}
			continue
		}

		var msg struct {
			Type string `json:"type"`
			Cols *int   `json:"cols"`
			Rows *int   `json:"rows"`
		}
		if json.Unmarshal(data, &msg) != nil || msg.Type != "resize" || msg.Cols == nil || msg.Rows == nil ||
			*msg.Cols < 1 || *msg.Cols > maxTerminalCols || *msg.Rows < 1 || *msg.Rows > maxTerminalRows {
			closeWith(conn, statusBadControl, "unsupported control message")
			return
		}
		rctx, cancel := context.WithTimeout(ctx, ptyOpenTimeout)
		err = term.Resize(rctx, *msg.Cols, *msg.Rows)
		cancel()
		if err != nil {
			if writeControl(ctx, conn, map[string]any{"type": "error", "error": "resize failed: " + err.Error()}) != nil {
				return
			}
		}
	}
}

func closeForAttachError(conn *websocket.Conn, err error) {
	var nojob *daemon.ErrNojob
	var auth *daemon.ErrAuth
	switch {
	case errors.As(err, &nojob):
		closeWith(conn, statusNoSuchSession, "no such session")
	case errors.As(err, &auth):
		closeWith(conn, statusKeyRefused, "the daemon refused the control key")
	case errors.Is(err, daemon.ErrDaemonUnavailable):
		closeWith(conn, statusDaemonUnavailable, "daemon unavailable")
	default:
		closeWith(conn, websocket.StatusInternalError, "attach failed: "+err.Error())
	}
}

func closeWith(conn *websocket.Conn, code websocket.StatusCode, reason string) {
	_ = conn.Close(code, truncateReason(reason))
}

// truncateReason cuts reason to what a close frame can carry without splitting a rune.
func truncateReason(reason string) string {
	if len(reason) <= maxCloseReason {
		return reason
	}
	cut := maxCloseReason
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut]
}

func writeControl(ctx context.Context, conn *websocket.Conn, msg map[string]any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, data)
}
