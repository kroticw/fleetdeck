// Package server exposes the panel's snapshot over HTTP and WebSocket and accepts
// every write the panel performs.
//
// Deliberately not a list, and deliberately not a count. This comment said "the
// four writes" while there were seven routes answering a write method, because
// each of the three additions since — a configuration patch, a session label, an
// image — was made without anyone thinking to come back here. A sentence naming
// a number goes stale on somebody else's change and keeps sounding authoritative
// while it does. New() below is the list, it cannot drift from itself, and spec
// section 5 is where the kinds of write are argued rather than enumerated.
//
// It performs no I/O of its own beyond the connection it is answering. Every source
// it needs — the assembled snapshot, the daemon client, the board writer, the store
// that keeps statusline reports — arrives as a function in Deps, which is what makes
// the whole HTTP surface testable with no daemon, no board directory and no browser.
// Wiring those functions to the real thing is the caller's job (Task 11).
package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kroticw/fleetdeck/internal/buildinfo"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/transcript"
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

// ErrCardWrittenNotCommitted is ErrFieldWrittenNotCommitted for a new card: the
// card file exists and the commit did not happen. Reported as a failure, the
// operator would create the card again and get a second one.
var ErrCardWrittenNotCommitted = errors.New("card written but not committed")

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

	// CreateCard starts a card on the board from a title and a zone, and records
	// it in the board's git history if the caller wired it to do so. It returns
	// the new card's path. internal/board decides what a valid title and zone
	// are; this server maps its refusals onto status codes. An error wrapping
	// ErrCardWrittenNotCommitted, returned with the path, means the card exists
	// and only the commit did not happen, and is answered as a success.
	//
	// Nothing from the request names a file: the caller creates the card in its
	// own board, under a name made from the title.
	CreateCard func(title, zone string) (string, error)

	// CreateFleet makes a fleet from the start page: the folder with its board
	// and documentation, and the line in the configuration naming them. It
	// reports the steps setting up reports, and ok says whether the fleet is
	// there. An error means the request itself was refused before any step was
	// taken — an empty name, or a folder that is not a full path — and nothing
	// was made.
	//
	// A name or a board the configuration would not take is NOT an error here:
	// it comes back as a step that was refused, with ok false, because that is
	// where `fleetdeck init --fleet` puts it and this route runs the same
	// steps. The page shows it in the report rather than beside the form.
	//
	// A panel wired without it makes no fleets and says so (a stand). The fleet
	// it makes is served after a restart, never at once: see handleCreateFleet.
	CreateFleet func(name, path string) (steps []SetupStep, ok bool, err error)

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

	// DocsRoots are the directories the documentation section reads from, and the
	// only directories a document request may resolve into. Like BoardDir, this is
	// not a capability the panel simply lacks when it is empty: a document path
	// arrives from the browser, and without roots there is nothing to confine it
	// to. Both documentation routes answer 404 saying no roots are configured
	// rather than reading an unconfined path or showing an empty list, which would
	// claim there is no documentation instead of that none was configured.
	DocsRoots []string

	// PutStatus records a statusline reporter's report. It cannot fail: the
	// reporter never reads the response and a panel that cannot store a report
	// has nothing useful to say about it, so the receiver's only job is to refuse
	// a report it could not attribute to a session.
	PutStatus func(sessionID, model string, contextPercent, costUSD float64)

	// Digest returns up to limit of the most recent readable steps of a
	// session's transcript, oldest first. sessionID is the transcript UUID
	// (daemon.Session.SessionID), the identifier transcript.Locate matches on
	// — never the daemon's short id.
	Digest func(sessionID string, limit int) ([]transcript.Step, error)

	// SetOrchestratorSession pins (or, given an empty string, unpins) the session
	// shown in the orchestrator column, and persists the choice to the
	// configuration file. A nil value means a panel wired without a
	// configuration store; the route then answers 503.
	SetOrchestratorSession func(id string) error

	// SetSessionLabel writes (or, given an empty label, deletes) the
	// operator's own name for one session, persisted to the configuration
	// file's session_labels map — see internal/config.SetSessionLabel,
	// which this is expected to wrap. sessionID is the transcript UUID
	// (daemon.Session.SessionID), matching handleDigest's own convention —
	// never the daemon's short id, which is reassigned on every restart and
	// cannot durably name anything.
	//
	// No frontend calls this route yet. It exists so the panel's next
	// change — showing operator-assigned names once the neighbouring
	// orchestrator/sessions restructure has landed — has something to write
	// to; until then the label is stored and served in every snapshot
	// (state.SessionView.Label) but rendered nowhere.
	SetSessionLabel func(sessionID, label string) error

	// OrchestratorPreview and Appoint are the orchestrator wizard's two routes
	// (orchestrator.go): what an appointment would write and send, and the
	// appointment itself — internal/orchestrator.Appointer's Preview and
	// Appoint. Nil leaves both answering 503.
	OrchestratorPreview func(lang string) (orchestrator.Preview, error)
	Appoint             func(ctx context.Context, req orchestrator.Request) (orchestrator.Result, error)

	// Fleet answers, for a fleet name from a request's fleet query parameter
	// ("" being the first fleet), the capabilities that fleet has in place of
	// BoardDir, DocsRoots, CreateCard, SetOrchestratorSession,
	// OrchestratorPreview and Appoint above, which are then the first
	// fleet's. An error wrapping fleet.ErrUnknown is a fleet no configuration
	// has, answered 404. Nil is a panel with one fleet, served from the fields
	// above as it always was. See fleetdeps.go.
	Fleet func(name string) (FleetDeps, error)

	// ImageDir is where an image attached to a session is written, under a
	// subdirectory named after that session. It is the panel's own directory,
	// deliberately outside any repository the operator works in: a file left in
	// a working tree eventually reaches somebody's commit, and that is not undone
	// by noticing it later.
	//
	// Like BoardDir, an empty value is not a capability the panel merely lacks —
	// it is the absence of anywhere safe to write, so the route answers 503
	// rather than choosing a directory on its own.
	ImageDir string

	// Build describes the running panel. It is stamped on every snapshot, and
	// its web hash is written into index.html in the same response that
	// delivers the page -- so the page knows which build it came from without
	// asking again, and learns from the socket when the panel under it has
	// changed. Nil serves the page as embedded and the snapshot without it.
	Build *buildinfo.Fingerprint

	// Attach opens a held, two-way terminal on a session at the given geometry —
	// daemon.Client.Attach. It backs GET /api/sessions/{id}/pty. ctx bounds opening
	// only; the terminal lives until it is closed. Nil leaves the route answering 503.
	Attach func(ctx context.Context, session string, cols, rows int) (Terminal, error)

	// SessionListed reports whether the daemon lists session as alive right now —
	// present in its list and not dying. The terminal bridge asks it when a stream
	// ends with no reason attached, because that alone does not say the session
	// ended (see streamEnding in pty.go). It must ask the daemon, not the cached
	// Snapshot, which can be a whole poll behind. Nil leaves such an ending
	// unexplained rather than guessed.
	SessionListed func(ctx context.Context, session string) (bool, error)

	// TerminalToken is the secret a terminal socket must present as its first
	// message before the bridge attaches to anything (see authenticateTerminal in
	// pty.go). It is expected to be random and to live exactly as long as the
	// process: the panel's page reads it from GET /api/terminal-token each time it
	// opens a terminal, so a restart costs nothing the restart has not already
	// cost — every socket the old process held is gone with it. It is never
	// written to disk, logged, or put in a snapshot.
	//
	// Like BoardDir, an empty value is not a capability the panel simply lacks:
	// without it the terminal socket would have only the Origin rule between a
	// page and a live session, so both routes answer 503 instead.
	TerminalToken string

	// terminalAuthTimeout overrides how long a terminal socket may take to send
	// its token. It exists for tests, which cannot wait the real ten seconds; zero
	// means terminalAuthWait.
	terminalAuthTimeout time.Duration

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
	// Stamped here, once, so the HTTP route and the socket cannot disagree:
	// both read snapshots through d.Snapshot, and the handlers below are bound
	// to this copy of d.
	if d.Snapshot != nil && d.Build != nil {
		collect, build := d.Snapshot, d.Build
		d.Snapshot = func() state.Snapshot {
			snap := collect()
			snap.Build = build
			return snap
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/snapshot", d.handleSnapshot)
	mux.HandleFunc("POST /api/sessions/{id}/text", d.handleSendText)
	mux.HandleFunc("PATCH /api/cards", d.handlePatchCard)
	mux.HandleFunc("POST /api/cards", d.handleCreateCard)
	mux.HandleFunc("POST /api/fleets", d.handleCreateFleet)
	mux.HandleFunc("GET /api/docs", d.handleDocsList)
	mux.HandleFunc("GET /api/docs/content", d.handleDocsContent)
	mux.HandleFunc("POST /api/status", d.handleStatus)
	mux.HandleFunc("GET /api/sessions/{id}/digest", d.handleDigest)
	mux.HandleFunc("PATCH /api/config", d.handlePatchConfig)
	mux.HandleFunc("PATCH /api/sessions/{id}/label", d.handleSetSessionLabel)
	mux.HandleFunc("GET /api/orchestrator", d.handleOrchestratorPreview)
	mux.HandleFunc("POST /api/orchestrator", d.handleAppoint)
	mux.HandleFunc("POST /api/sessions/{id}/image", d.handleUploadImage)
	mux.HandleFunc("GET /ws", d.handleWS)
	mux.HandleFunc("GET /api/sessions/{id}/pty", d.handlePTY)
	mux.HandleFunc("GET /api/terminal-token", d.handleTerminalToken)
	mux.Handle("GET /", staticHandler(d.Build))
	return guard(mux)
}
