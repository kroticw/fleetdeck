package server

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// writeTimeout bounds a single push. Without it one browser that stopped reading
// — a laptop asleep with the tab open, a paused debugger — would leave this
// handler blocked in a write for as long as the kernel's send buffer took to fill
// and never drain, holding a goroutine and a connection per stalled tab.
const writeTimeout = 5 * time.Second

// defaultPushInterval is the cadence spec section 4 asks of the panel: the whole
// snapshot, once a second.
const defaultPushInterval = time.Second

// handleWS pushes a fresh snapshot to the browser once a second, starting with one
// on connect so a freshly opened panel is not blank for the first tick.
//
// The socket is read-only. Every write into the fleet — text, keys, card fields —
// goes through the HTTP routes instead, so validation and error reporting have one
// path and one place to be tested. readOnly below enforces that: it answers ping,
// pong and close frames, and closes the connection outright if the client sends a
// data message, rather than silently ignoring it.
//
// Origins are restricted to the loopback addresses the panel itself is served
// from. The server listens on 127.0.0.1 only (spec section 8), but that alone
// stops nothing: a page on any site the operator visits can open a socket to their
// own loopback, and without this check it could read the entire fleet's state —
// prompts, card contents, session names — straight out of their browser.
//
// That check is originAllowed, the same function every HTTP route goes through.
// It runs here as well as in guard so the socket stays closed to foreign pages
// even if this handler is ever mounted without the middleware, and the library's
// own origin verification is turned off so there is exactly one rule in the
// package and no second one to drift away from it.
func (d Deps) handleWS(w http.ResponseWriter, r *http.Request) {
	if d.Snapshot == nil {
		unavailable(w, "a snapshot source")
		return
	}
	if !originAllowed(r) {
		refuseForeignOrigin(w)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Accept has already answered the request (400 for a malformed
		// handshake); there is nothing left to write.
		return
	}
	defer func() { _ = conn.CloseNow() }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go readOnly(ctx, cancel, conn)

	ticker := time.NewTicker(d.pushInterval())
	defer ticker.Stop()
	for {
		if !d.push(ctx, conn) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// readOnly drains the client's side of a socket that is not supposed to have one.
// Somebody has to read: without a reader the library never sees a ping, a pong or
// the close frame a browser sends when its tab goes away, and the push loop would
// keep writing into a connection nobody is on the other end of until a write
// finally failed. It cancels the push loop's context on the way out, so a closed
// tab ends the handler at once rather than at the next tick.
//
// A data message means the client is trying to write through a socket this server
// does not accept writes on, so the connection is closed with a status that says
// exactly that.
//
// This is what the library's own CloseRead does, minus one detail that matters
// here: CloseRead takes a Reader and never drains it, so the message body keeps
// the read lock held and the close handshake that follows blocks on its own lock
// until a 15-second timeout expires — pinning this goroutine and the connection
// for those 15 seconds. Read consumes the message, so the close completes at once.
func readOnly(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	defer cancel()
	if _, _, err := conn.Read(ctx); err != nil {
		// The client closed, went away, or sent something unreadable. Either way
		// there is nothing left to push to.
		return
	}
	_ = conn.Close(websocket.StatusPolicyViolation, "this socket is read-only: write through the HTTP API")
}

// push sends one snapshot and reports whether the connection is still usable. A
// failed write ends the loop: the connection is gone, or the browser stopped
// reading, and either way there is nobody left to push to.
func (d Deps) push(ctx context.Context, conn *websocket.Conn) bool {
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(ctx, conn, d.Snapshot()) == nil
}

func (d Deps) pushInterval() time.Duration {
	if d.interval > 0 {
		return d.interval
	}
	return defaultPushInterval
}
