// Package server exposes the panel's snapshot over HTTP and WebSocket and accepts
// the four writes the panel performs: text into a session, keys into a session, one
// field of one card, and a statusline reporter's report.
//
// It performs no I/O of its own beyond the connection it is answering. Every source
// it needs — the assembled snapshot, the daemon client, the board writer, the store
// that keeps statusline reports — arrives as a function in Deps, which is what makes
// the whole HTTP surface testable with no daemon, no board directory and no browser.
// Wiring those functions to the real thing is the caller's job (Task 11).
package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
)

// ErrFieldWrittenNotCommitted is what SetCardField wraps when the field reached
// the card file but the commit did not happen. The edit is applied; only the git
// history is missing it.
//
// It exists because a card write is two steps and only the first one decides
// whether the operator's edit took effect. Once the file is written, any later
// failure — a signing passphrase prompt with nobody to answer it, a repository in
// a state git will not commit from — leaves the panel holding an error for
// something that already happened. Reported as a plain failure, the operator
// redoes an edit that took effect, and a progress field applied twice moves
// somewhere nobody asked for.
var ErrFieldWrittenNotCommitted = errors.New("field written but not committed")

// Deps holds everything the HTTP surface needs from the rest of the program.
//
// Every field is optional. A nil function means a panel wired without that
// capability — a configuration fact, not a bug — and its routes answer 503 rather
// than panicking. The routes are registered either way, so the answer is an
// explicit "this panel does not do that" instead of an indistinguishable 404.
type Deps struct {
	// Snapshot returns the panel's current view of the fleet. It is called on
	// every /api/snapshot request and once per push on the WebSocket, so it must
	// be cheap: the caller is expected to hand over a cached snapshot refreshed
	// on its own cadence, not to poll the daemon from inside it.
	Snapshot func() state.Snapshot

	// SendText types text into a session and submits it. There is no submit
	// parameter because the daemon's reply operation has no such option — see
	// daemon.Client.SendText.
	SendText func(session, text string) error

	// SendKeys writes raw key bytes into a session's terminal.
	SendKeys func(session, keys string) error

	// ReadScreen reads the tail of a session's terminal. It returns
	// daemon.ScreenResult rather than (string, error) so a failed read cannot
	// silently discard the bytes it did collect.
	ReadScreen func(session string, tail int) daemon.ScreenResult

	// SetCardField writes one field of one card, and records it in the board's git
	// history if the caller wired it to do so. The server does not decide which
	// fields the panel owns or which values are legal — internal/board does, and
	// this server only maps its refusals onto status codes.
	//
	// Two of those returns carry a meaning the status codes depend on. An error
	// wrapping ErrFieldWrittenNotCommitted means the field reached the card and
	// only the commit did not happen, and is answered as a success. An error
	// wrapping board.ErrNothingToCommit means the card already held the value, and
	// is answered as an ordinary success. Everything else is a failed write.
	//
	// path arrives from the browser and is confined to BoardDir before this
	// function is called, so what it receives is always an absolute path that
	// resolves to somewhere inside the board.
	SetCardField func(path, field, value string) error

	// BoardDir is the only directory card writes may touch. Every path a card
	// write arrives with is resolved and checked against it, and anything that
	// lands elsewhere is refused before board.SetField — which will rewrite a
	// frontmatter line in any file that has one — ever sees it.
	//
	// Unlike the functions above, an empty BoardDir is not a capability the panel
	// simply lacks: SetCardField without it is a write with nothing confining it.
	// The card route answers 503 in that case rather than guessing where the
	// board is.
	BoardDir string

	// PutStatus records a statusline reporter's report. It cannot fail: the
	// reporter never reads the response and a panel that cannot store a report
	// has nothing useful to say about it, so the receiver's only job is to refuse
	// a report it could not attribute to a session.
	PutStatus func(sessionID, model string, contextPercent, costUSD float64)

	// interval overrides the WebSocket's one-second push cadence. It exists for
	// tests, which cannot afford to wait whole seconds to observe a cadence; zero
	// means the one second the panel actually uses.
	interval time.Duration
}

// New builds the router. Routing uses net/http's own pattern matching (Go 1.22+),
// which also supplies the 405 for a known path reached with the wrong method — no
// third-party mux is involved.
//
// Every route, read and write alike, is wrapped in guard: the origin and content
// type checks that keep a page on another site from driving this panel through
// the operator's own browser.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/snapshot", d.handleSnapshot)
	mux.HandleFunc("POST /api/sessions/{id}/text", d.handleSendText)
	mux.HandleFunc("POST /api/sessions/{id}/keys", d.handleSendKeys)
	mux.HandleFunc("GET /api/sessions/{id}/screen", d.handleScreen)
	mux.HandleFunc("PATCH /api/cards", d.handlePatchCard)
	mux.HandleFunc("POST /api/status", d.handleStatus)
	mux.HandleFunc("GET /ws", d.handleWS)
	return guard(mux)
}
