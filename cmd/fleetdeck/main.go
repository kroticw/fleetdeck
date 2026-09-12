// Command fleetdeck serves the fleet control panel on the loopback interface.
//
// This is where every internal/ package meets: the daemon client, the board reader
// and writer, the transcript reader, the usage fetcher, the snapshot and its
// notification rules, the notifier, and the HTTP and WebSocket surface. None of them
// knows about any of the others; the wiring lives here and nowhere else.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/buildinfo"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/notify"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
	"github.com/kroticw/fleetdeck/internal/version"
	"github.com/kroticw/fleetdeck/web"
)

const (
	// readHeaderTimeout bounds how long a client may take to send its request
	// headers. Without it a handful of connections dribbling one byte at a time
	// occupy the panel indefinitely.
	//
	// There is deliberately no ReadTimeout or WriteTimeout alongside it: both apply
	// to the whole connection, and the panel's /ws route is a WebSocket that is meant
	// to stay open for as long as the browser tab does. A write deadline set before
	// the handler runs survives the hijack the WebSocket performs, so a WriteTimeout
	// here would sever every live terminal at a fixed age. The handlers bound
	// themselves instead — the daemon client carries its own deadlines and
	// internal/board bounds every git invocation.
	readHeaderTimeout = 10 * time.Second

	// idleTimeout closes a kept-alive connection that has gone quiet between
	// requests. It does not apply to a connection the WebSocket has hijacked.
	idleTimeout = 2 * time.Minute

	// shutdownTimeout bounds the graceful shutdown. Past it the process stops
	// waiting for in-flight requests — a card write blocked on a signing passphrase
	// prompt with nobody at the keyboard must not keep the panel alive forever.
	shutdownTimeout = 5 * time.Second

	// usageTTL is how long the account limits are cached. Spec section 3.2: one
	// request serves every session rather than one request per session.
	usageTTL = time.Minute
)

// panel owns the collect-and-diff cycle and the snapshot the HTTP surface serves.
//
// The cycle has two callers — the poll ticker and the board watcher — so it is one
// function behind one mutex rather than a loop body copied into both. Two cycles
// running at once would each diff against a snapshot the other had already replaced,
// which either fires a banner twice or loses one entirely.
type panel struct {
	collect      func(context.Context) state.Snapshot
	banners      banner
	notifyCfg    config.NotifyConfig
	onNotifyFail func(error)

	// cycleMu serialises the cycle itself. snapMu guards only the published
	// snapshot, so an HTTP request never waits behind a collect that is part way
	// through reading a transcript from disk.
	cycleMu sync.Mutex
	snapMu  sync.RWMutex
	snap    state.Snapshot
}

func newPanel(collect func(context.Context) state.Snapshot, b banner, cfg config.NotifyConfig, onNotifyFail func(error)) *panel {
	return &panel{collect: collect, banners: b, notifyCfg: cfg, onNotifyFail: onNotifyFail}
}

// refresh runs one cycle: collect, publish, diff against what was published before,
// and deliver the result.
//
// It does nothing at all once ctx is done. The board watcher coalesces bursts through
// a timer, so its callback can still fire while the process is shutting down, and a
// cycle started then would be collecting from sources that are being torn down and
// raising banners about a fleet nobody is watching any more.
func (p *panel) refresh(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	p.cycleMu.Lock()
	defer p.cycleMu.Unlock()

	next := p.collect(ctx)

	p.snapMu.Lock()
	prev := p.snap
	p.snap = next
	p.snapMu.Unlock()

	fire, cleared := state.Diff(prev, next, p.notifyCfg.SilenceAfter)
	deliver(p.banners, p.notifyCfg, fire, cleared, p.onNotifyFail)
}

// snapshot is what server.Deps.Snapshot hands out: the last cycle's result, already
// assembled, never a fresh poll of the daemon.
func (p *panel) snapshot() state.Snapshot {
	p.snapMu.RLock()
	defer p.snapMu.RUnlock()
	return p.snap
}

// poll runs refresh once and then on every tick until ctx is done.
//
// The first run is before the ticker rather than on its first tick: otherwise the
// panel serves an empty snapshot with a zero At for a whole poll interval after
// startup, and the browser cannot tell that from a fleet with nothing running.
func poll(ctx context.Context, interval time.Duration, refresh func(context.Context)) {
	refresh(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh(ctx)
		}
	}
}

// watchBoard runs board.Watch on boardDir's cards subdirectory (board.CardsDir)
// so an edit made by an agent or in the operator's own editor reaches the panel
// when it happens rather than up to a poll interval later (spec section 5: the
// board is ordinary markdown that anything may edit). This must watch the same
// directory Collect's board.Scan call actually reads, or a card edit would wake
// the panel to re-scan a directory with nothing new in it.
//
// A board that cannot be watched — unconfigured, missing, removed while running —
// does not take the panel down. It is reported once and the panel goes on polling,
// the same way an unreachable daemon does not stop the board from being served.
func watchBoard(ctx context.Context, boardDir string, onChange func()) {
	if boardDir == "" {
		return
	}
	if err := board.Watch(ctx, board.CardsDir(boardDir), onChange); err != nil {
		log.Printf("board watch: %v (the panel keeps running; board edits will be picked up by the next poll)", err)
	}
}

// setCardField is server.Deps.SetCardField: write one field of one card, then record
// it in the board's git history.
//
// The two steps have three distinct outcomes and the server answers each one
// differently, so this function must not flatten them into a single error:
//
//   - the write itself failed — returned as it is, and the server maps it onto a
//     status code from what internal/board said;
//   - the write succeeded and there was nothing to commit, because the card already
//     held the value — board.ErrNothingToCommit passes through unwrapped, and the
//     server answers an ordinary success;
//   - the write succeeded and the commit did not happen — wrapped in
//     server.ErrFieldWrittenNotCommitted, which the server answers as a success that
//     says the commit is missing. Reported as a plain failure this is the one that
//     does damage: the operator redoes an edit that already took effect, and a
//     progress field applied twice moves somewhere nobody asked for.
func setCardField(path, field, value string) error {
	if err := board.SetField(path, field, value); err != nil {
		return err
	}
	msg := fmt.Sprintf("chore(board): set %s to %s", field, value)
	err := board.Commit(filepath.Dir(path), filepath.Base(path), msg)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, board.ErrNothingToCommit):
		return err
	default:
		return fmt.Errorf("%w: %w", server.ErrFieldWrittenNotCommitted, err)
	}
}

// createCard is server.Deps.CreateCard: start a card on the board, then record it
// in the board's git history — the same two steps as setCardField, with the same
// rule for the second: once the file exists, a commit that did not happen is
// wrapped in server.ErrCardWrittenNotCommitted and returned with the path, so the
// operator is not invited to create the card a second time.
func createCard(boardDir, title, zone string, now time.Time) (string, error) {
	path, err := board.CreateCard(boardDir, title, zone, now)
	if err != nil {
		return "", err
	}
	msg := "chore(board): add card " + filepath.Base(path)
	if err := board.Commit(filepath.Dir(path), filepath.Base(path), msg); err != nil {
		return path, fmt.Errorf("%w: %w", server.ErrCardWrittenNotCommitted, err)
	}
	return path, nil
}

// setOrchestratorSession is server.Deps.SetOrchestratorSession: pin, or
// given an empty string unpin, the session shown in the orchestrator
// column, persisting the choice before the running collector reports it.
//
// The write goes through config.SetField first, which touches only the one
// line naming orchestrator.session and leaves every comment and every other
// byte of a hand-edited config alone. Falling back to a full config.Save
// happens only when that file does not exist at all — there is nothing
// hand-written to lose on a config nobody has created yet, and this is the
// same fallback `fleetdeck init` itself uses to create one in the first
// place. Any other failure (a config that exists but is missing the section
// that would hold this key, an unparseable rewrite) is returned as it is,
// never silently upgraded to a full rewrite that would destroy exactly the
// comments SetField exists to protect.
func setOrchestratorSession(configPath string, collector *Collector, id string) error {
	err := config.SetField(configPath, "orchestrator.session", id)
	if errors.Is(err, fs.ErrNotExist) {
		next := collector.Config()
		next.OrchestratorSession = id
		err = config.Save(configPath, next)
	}
	if err != nil {
		return err
	}
	collector.SetOrchestratorSession(id)
	return nil
}

// setSessionLabel is server.Deps.SetSessionLabel: write, or given an empty
// label remove, the operator's own name for one session, persisting the
// choice before the running collector reports it next.
//
// The write goes through config.SetSessionLabel first — the surgical,
// comment-preserving path setOrchestratorSession uses for its own key,
// extended to a dynamically-keyed map — falling back to a full config.Save
// only when the file does not exist at all, for the same reason
// setOrchestratorSession does: nothing hand-written to lose on a config
// nobody has created yet. Any other failure (a malformed sessionID, a
// newline in the label, a config missing the session_labels section
// entirely) is returned as it is, never silently upgraded to a full rewrite
// that would destroy exactly the comments SetSessionLabel exists to
// protect.
func setSessionLabel(configPath string, collector *Collector, sessionID, label string) error {
	err := config.SetSessionLabel(configPath, sessionID, label)
	if errors.Is(err, fs.ErrNotExist) {
		// next.SessionLabels is collector.Config()'s own clone, not the
		// live map c.cfg.SessionLabels — mutating it here is safe and
		// touches nothing another goroutine can also be touching.
		next := collector.Config()
		if label == "" {
			delete(next.SessionLabels, sessionID)
		} else {
			if next.SessionLabels == nil {
				next.SessionLabels = map[string]string{}
			}
			next.SessionLabels[sessionID] = label
		}
		err = config.Save(configPath, next)
	}
	if err != nil {
		return err
	}
	collector.SetSessionLabel(sessionID, label)
	return nil
}

func main() {
	// The subcommand is read before any flag is defined or parsed: `fleetdeck init`
	// has flags of its own (--board, --force) and none of the panel's, and
	// flag.Parse would reject them as unknown before init ever ran. Without a
	// subcommand the panel starts exactly as it always has.
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if err := initCommand(os.Args[2:]); err != nil {
			log.Fatalf("fleetdeck init: %v", err)
		}
		return
	}

	// `fleetdeck version` is read here for the same reason, and answered by the same
	// function as the --version flag below.
	if len(os.Args) > 1 && os.Args[1] == "version" {
		runVersion(os.Stdout)
		return
	}

	configPath := flag.String("config", config.DefaultPath(), "path to the configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	standSocket := flag.String("stand-socket", "", "fixed daemon control-socket path for an isolated test stand: given, this panel connects ONLY to this socket and never discovers the real fleet daemon (see internal/daemon.New); required for a panel run anywhere a live fleet daemon might otherwise be found")
	ownerPID := flag.Int("owner-pid", 0, "the fleetdeck window that started this panel, as its parent: the panel stops when that process is gone")
	port := flag.Int("port", 0, "port to listen on instead of server.port; for a test stand, which must not take the operator's port even before it has a configuration")
	standClaude := flag.String("stand-claude", "", "the claude an isolated test stand starts sessions with; only with -stand-socket, and without it a stand starts no sessions at all, because a real claude reaches the real fleet daemon whatever socket the panel reads")
	flag.Parse()

	// -stand-socket is the one flag whose mere presence changes what this
	// panel is allowed to touch, so an accidentally empty value (a script
	// templating an unset variable into `-stand-socket=`) must not be read
	// as "flag not given" and fall through to discovering the real daemon —
	// see checkStandSocket and daemonClient's own docs for why that
	// fallback does not otherwise exist as code to reach.
	standSocketGiven, standClaudeGiven := false, false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "stand-socket":
			standSocketGiven = true
		case "stand-claude":
			standClaudeGiven = true
		}
	})
	if err := checkStandSocket(standSocketGiven, *standSocket); err != nil {
		log.Fatalf("fleetdeck: %v", err)
	}
	if err := checkStandClaude(standSocketGiven, standClaudeGiven, *standClaude); err != nil {
		log.Fatalf("fleetdeck: %v", err)
	}

	if *showVersion {
		runVersion(os.Stdout)
		return
	}

	if err := runWith(runOpts{configPath: *configPath, standSocket: *standSocket, standClaude: *standClaude, owner: *ownerPID, port: *port}); err != nil {
		log.Fatalf("fleetdeck: %v", err)
	}
}

// imagesDir is where an image attached to a session is kept: the panel's own
// directory under the user's home, never a directory the operator's work lives
// in.
//
// An empty string when the home directory cannot be determined, which the server
// reads as "not wired for this" and answers 503 — the one thing it must not do
// is fall back to a relative path, which would put the files wherever the panel
// happened to be started from.
func imagesDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "fleetdeck", "images")
}

// projectsDir is where Claude Code keeps session transcripts.
func projectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// run assembles the panel and serves it until a signal arrives.
//
// Only two things can stop it from starting: a configuration file that exists and
// does not parse, and a port it cannot listen on. Everything else degrades — a daemon
// that is not running, a board that is not there, a usage endpoint that has changed
// shape — because a panel that refuses to start when one source is down is a panel
// that cannot be used to find out which source is down. (main's own flag parsing
// adds a third, earlier one — an empty -stand-socket — before configPath even
// reaches here.)
//
// owner is the PID of the window that started the panel, or 0. With an owner,
// the panel shuts down the same way it does on SIGTERM once that process is
// gone (see owner.go).
func run(configPath, standSocket string, owner int) error {
	return runWith(runOpts{configPath: configPath, standSocket: standSocket, owner: owner})
}

// runOpts is what the command line hands the panel.
type runOpts struct {
	configPath, standSocket string
	// standClaude is the claude a stand starts sessions with; see
	// sessionStarter.
	standClaude string
	// owner is the window that started the panel, or 0.
	owner int
	// port, when not 0, is listened on instead of server.port. A panel with no
	// configuration file has no server.port of its own but the default, and a
	// test stand must not take the default: it belongs to the operator's panel.
	port int
}

// runWith is run with every option, serving until a signal arrives.
func runWith(o runOpts) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return serve(ctx, o)
}

// serve is the panel: it assembles everything and serves until ctx is done.
//
// A panel with no configuration file starts in setup: it listens, serves the
// setup page, and once a workspace and a configuration naming it have been made
// (setupWorkspace), it loads that configuration and becomes the panel on the
// same listener, without a restart — the window watching it sees no gap. A
// panel that has a configuration never offers setup.
func serve(parent context.Context, o runOpts) error {
	if o.owner != 0 {
		if err := checkOwner(o.owner); err != nil {
			return err
		}
	}
	firstRun, cfg, err := loadConfig(o.configPath)
	if err != nil {
		return err
	}

	ctx, stop := context.WithCancel(parent)
	defer stop()
	if o.owner != 0 {
		gone := ownerGone(o.owner)
		go func() {
			select {
			case <-gone:
				log.Printf("fleetdeck: the window that started this panel (pid %d) is gone; shutting down with it", o.owner)
				stop()
			case <-ctx.Done():
			}
		}()
	}

	port := cfg.ServerPort
	if o.port != 0 {
		port = o.port
	}
	// Bind before announcing anything: a bare fmt.Sprintf("127.0.0.1:%d", ...)
	// printed ahead of ListenAndServe made a failed start look like a running
	// panel — the log carried the success line and then an unrelated-looking
	// bind error, right next to whichever process actually holds the port
	// (most often another panel: the one the fleetdeck window started from
	// inside its app bundle, or one started from a terminal). ln.Addr() is
	// used for the log line rather than the address that was asked for so
	// that server.port: 0 — "let the OS choose" — reports the port it
	// actually got, not literally "0".
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return bindError(addr, err, portHolder(context.Background(), addr), readBuild())
		}
		return fmt.Errorf("bind %s: %w", addr, err)
	}

	var handler switchHandler
	srv := &http.Server{
		Handler:           &handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	serveErr := make(chan error, 1)
	startServing := func(what string) {
		go func() {
			log.Print(servingLine(version.String(), what, ln.Addr().String(), buildinfo.Executable()))
			serveErr <- srv.Serve(ln)
		}()
	}

	if firstRun {
		ready := make(chan struct{})
		handler.set(server.NewSetup(setupDeps(o.configPath, ready)))
		startServing("waiting to be set up")
		select {
		case <-ready:
		case err := <-serveErr:
			return err
		case <-ctx.Done():
			return shutdown(srv)
		}
		if cfg, err = config.Load(o.configPath); err != nil {
			_ = shutdown(srv)
			return err
		}
		log.Printf("fleetdeck: set up with the board at %s", cfg.BoardPath)
	}

	dc := daemonClient(o.standSocket)
	uf := usage.NewFetcher(usage.KeychainToken, usage.Endpoint, usageTTL)
	collector := NewCollector(cfg, dc, uf, projectsDir())

	p := newPanel(
		collector.Collect,
		notify.New(notify.OSAScriptSend),
		cfg.Notify,
		func(err error) { log.Printf("notify: %v", err) },
	)

	d := deps(ctx, p, dc, collector, cfg, o.configPath)
	// Every fleet's board, docs, pin and wizard, the first fleet's also in
	// d's own fields, so a request naming no fleet is served as before. Made
	// before anything below is started, so there is nothing to stop if it fails.
	d.Fleet = newFleets(o, cfg, dc, collector)
	first, err := d.Fleet("")
	if err != nil {
		return err
	}
	d.OrchestratorPreview, d.Appoint, d.SetOrchestratorSession = first.OrchestratorPreview, first.Appoint, first.SetOrchestratorSession

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		poll(ctx, cfg.DaemonPollInterval, p.refresh)
	}()
	// Every fleet's board is watched, not only the first: a card moved on any
	// of them raises its banner at once rather than at the next poll.
	for _, f := range cfg.FleetList() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			watchBoard(ctx, f.BoardPath, func() { p.refresh(ctx) })
		}()
	}
	if d.Build != nil {
		// Which window this panel belongs to, for a window that finds it answering.
		d.Build.Owner = o.owner
	}
	handler.set(server.New(d))
	if !firstRun {
		startServing("listening")
	}

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			stop()
			wg.Wait()
			return err
		}
	case <-ctx.Done():
		log.Print("fleetdeck: shutting down")
	}

	// Stop the ticker and the watcher first, so nothing starts a fresh cycle while
	// the server is draining.
	stop()
	wg.Wait()
	return shutdown(srv)
}

// servingLine is the log line a panel starts serving with. It names the binary
// serving, not only the version and the address: one update starts two panels
// of the same version on the same address, first from the staged bundle and
// then from the canonical one, and without the path the two starts read as
// two crashes (docs/engineering/window-and-panel.md).
func servingLine(ver, what, addr, exe string) string {
	line := fmt.Sprintf("fleetdeck %s %s on http://%s", ver, what, addr)
	if exe != "" {
		line += " from " + exe
	}
	return line
}

func shutdown(srv *http.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// loadConfig reads the configuration file. firstRun is true when there is no
// file at all: that, and only that, is a panel that has never been set up. A
// file that exists and does not parse is an error, as it always was — never a
// reason to offer setup over somebody's configuration.
func loadConfig(path string) (firstRun bool, cfg config.Config, err error) {
	switch _, statErr := os.Stat(path); {
	case errors.Is(statErr, fs.ErrNotExist):
		return true, config.Default(), nil
	case statErr != nil:
		return false, config.Config{}, fmt.Errorf("read config %s: %w", path, statErr)
	}
	cfg, err = config.Load(path)
	return false, cfg, err
}

// switchHandler is the listener's one handler, replaced once: the setup surface
// first, the panel after it.
type switchHandler struct {
	h atomic.Pointer[http.Handler]
}

func (s *switchHandler) set(h http.Handler) { s.h.Store(&h) }

func (s *switchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := s.h.Load()
	if h == nil {
		http.Error(w, "fleetdeck is starting", http.StatusServiceUnavailable)
		return
	}
	(*h).ServeHTTP(w, r)
}

// setupDeps is the setup surface's one write: `fleetdeck init --workspace`,
// the same steps and the same report, against the configuration file this
// panel was told to read. ready is closed once a configuration and a board
// exist, and a second setup is refused from then on.
func setupDeps(configPath string, ready chan struct{}) server.SetupDeps {
	home, _ := os.UserHomeDir()
	binary, _ := os.Executable()
	var mu sync.Mutex
	done := false
	return server.SetupDeps{
		DefaultWorkspace: filepath.Join(home, defaultWorkspaceName),
		Setup: func(path string) ([]server.SetupStep, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if done {
				return nil, false, errors.New("this panel is already set up")
			}
			root, err := workspacePath(path, home)
			if err != nil {
				return nil, false, err
			}
			steps := initSteps(initEnv{home: home, binary: binary, workspace: root, config: configPath})
			out := reportedSteps(steps)
			// The configuration is what the panel runs on, and it is written only
			// over a board that exists (saveNewConfig), so its step alone says
			// whether the panel can run. The statusline and the permissions are
			// reported, and a refusal there does not keep the panel from running.
			ok := steps[0].err == nil
			if ok {
				done = true
				close(ready)
			}
			return out, ok, nil
		},
	}
}

// reportedSteps is how a sequence of writes reaches a page: the steps
// `fleetdeck init` prints, each with what it did or why it was refused. The
// setup surface and the start page report the same way because they perform
// the same writes.
func reportedSteps(steps []initStep) []server.SetupStep {
	out := make([]server.SetupStep, 0, len(steps))
	for _, s := range steps {
		step := server.SetupStep{Name: s.name, Note: s.note, Detail: s.detail}
		if s.err != nil {
			step.Error = s.err.Error()
		}
		out = append(out, step)
	}
	return out
}

// Steps whose refusal does not keep a fleet from existing: the statusline is
// Claude Code's, and the permission is what lets sessions write in the new
// folder. Both are reported; neither decides.
var fleetStepsThatDoNotDecide = map[string]bool{"statusline": true, "permissions": true}

// fleetMade reports whether the fleet is there, from the steps that made it.
//
// Every deciding step has to have succeeded, not only the one named "config".
// fleetSteps reports the configuration as kept — no error — when the fleet was
// already listed, and it decides that before it looks at whether the board step
// failed. So a fleet already in the configuration whose folder cannot be read
// leaves a config step with no error beside a board step with one, and reading
// only the config step would tell the operator "the fleet is made" directly
// above a line saying its workspace could not be.
func fleetMade(steps []initStep) bool {
	decided := false
	for _, s := range steps {
		if fleetStepsThatDoNotDecide[s.name] {
			continue
		}
		if s.err != nil {
			return false
		}
		decided = true
	}
	return decided
}

// fleetMaker is the start page's one write: `fleetdeck init --fleet`, the same
// steps and the same report, against the configuration file this panel reads.
//
// Serialised, because two of these at once would each read the configuration,
// each append a fleet to what they read, and the second would write over the
// first. The wizard's own write is on the setup surface, which no longer
// exists by the time this one can be reached.
//
// The fleet is not served until the panel is restarted, and nothing here
// pretends otherwise (internal/server/fleets.go says why).
func fleetMaker(configPath string) func(name, path string) ([]server.SetupStep, bool, error) {
	home, _ := os.UserHomeDir()
	binary, _ := os.Executable()
	var mu sync.Mutex
	return func(name, path string) ([]server.SetupStep, bool, error) {
		mu.Lock()
		defer mu.Unlock()
		named := strings.TrimSpace(name)
		if named == "" {
			return nil, false, errors.New("name the fleet")
		}
		root, err := workspacePath(path, home)
		if err != nil {
			return nil, false, err
		}
		steps := fleetSteps(configPath, initEnv{home: home, binary: binary, workspace: root, config: configPath, fleet: named})
		return reportedSteps(steps), fleetMade(steps), nil
	}
}

// workspacePath is the path the setup page sent, made absolute: "~" and "~/..."
// against home. Anything else relative is refused — it would be relative to
// wherever the panel was started, which the person choosing cannot see.
func workspacePath(path, home string) (string, error) {
	path = strings.TrimSpace(path)
	switch {
	case path == "":
		return "", errors.New("name a folder for the workspace")
	case path == "~":
		return home, nil
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:]), nil
	case !filepath.IsAbs(path):
		return "", fmt.Errorf("%q is not a full path: give one starting with / or ~/", path)
	}
	return filepath.Clean(path), nil
}

// checkStandSocket refuses an explicitly empty -stand-socket, which a script
// templating an unset variable into `-stand-socket=` would otherwise produce
// silently. given comes from flag.Visit, not from value == "", because the
// bug this guards against is specifically "the flag was passed, but with
// nothing in it" — "not passed at all" is the ordinary, unconfigured case
// every real panel runs as, and must stay silent.
func checkStandSocket(given bool, value string) error {
	if given && value == "" {
		return errors.New("-stand-socket was given empty; refusing to start rather than silently discover the real fleet daemon instead")
	}
	return nil
}

// daemonClient returns the client this panel talks to the daemon through.
//
// standSocket == "" (every real operator's panel, unconditionally) is
// daemon.Discover: it resolves the real, uid-scoped daemon socket and
// re-resolves it across restarts (spec section 7), because the daemon's
// socket directory name is not stable across them and a client bound to
// today's path would keep failing once the daemon comes back under a new
// one.
//
// standSocket != "" is a test stand run somewhere a live fleet daemon might
// otherwise be found (see docs/en/configuration.md's "Running an isolated
// stand" section for the one command that sets this up correctly). It never
// calls Discover or daemon.SocketPath at all: daemon.New binds to exactly
// this path for the client's whole lifetime and never re-resolves, so there
// is no code shared between the two branches below for a "not found at this
// path, try the default instead" fallback to live in. THAT absence is the
// actual guarantee against a stand ending up on the real fleet daemon — not
// a runtime check that could itself have a bug, but a code path that does
// not exist to be reached.
//
// Cost, stated here rather than only in a PR description because it is the
// kind of thing someone finds by hitting it, not by reading about it first:
// a client from daemon.New does not notice the daemon behind standSocket
// restarting under a new socket, unlike Discover. A one-shot acceptance
// stand never runs long enough to care; a long-lived test fleet built on
// this flag would need restarting alongside its daemon.
func daemonClient(standSocket string) *daemon.Client {
	if standSocket == "" {
		return daemon.Discover(daemon.ControlKey)
	}
	log.Printf("fleetdeck: daemon discovery disabled — bound to %s (-stand-socket), the real fleet daemon is not reachable from this panel", standSocket)
	return daemon.New(standSocket, daemon.ControlKey)
}

// listedAlive reports whether short is in the daemon's list and not dying. A dying
// session is being stopped: the daemon marks it so about a second before its
// terminal stream ends, which is exactly when the bridge asks.
func listedAlive(sessions []daemon.Session, short string) bool {
	for _, s := range sessions {
		if s.Short == short {
			return !s.Dying
		}
	}
	return false
}

// deps is the whole contract between this program and the HTTP surface. Every entry
// is a function internal/server calls and none of them reaches back here.
func deps(ctx context.Context, p *panel, dc *daemon.Client, collector *Collector, cfg config.Config, configPath string) server.Deps {
	// Left nil without a board: the route then answers that this panel has no
	// board, instead of creating cards relative to wherever the panel started.
	var create func(title, zone string) (string, error)
	if cfg.BoardPath != "" {
		create = func(title, zone string) (string, error) {
			return createCard(cfg.BoardPath, title, zone, time.Now())
		}
	}
	return server.Deps{
		Snapshot:    p.snapshot,
		CreateFleet: fleetMaker(configPath),
		SendText:    func(session, text string) error { return dc.SendText(ctx, session, text) },
		// Resuming reads the job store and the transcripts on every press
		// rather than off the snapshot: the button in front of the operator
		// was drawn from a reading that may be hours old, and a working
		// directory deleted since then is exactly the case this must not
		// dispatch into. See resume.go.
		ResumeSession: resumeSession(resumeDeps{
			jobStore: jobStoreDir,
			projects: projectsDir(),
			listed:   func(lctx context.Context) ([]daemon.Session, error) { return dc.ListSessions(lctx) },
			resume:   func(rctx context.Context, spec daemon.ResumeSpec) error { return dc.Resume(rctx, spec) },
		}),
		// Returned through a local, not directly: a nil *daemon.Attachment put straight
		// into the interface would be a non-nil Terminal holding nothing.
		Attach: func(actx context.Context, session string, cols, rows int) (server.Terminal, error) {
			a, err := dc.Attach(actx, session, cols, rows)
			if err != nil {
				return nil, err
			}
			return a, nil
		},
		// Asked of the daemon itself, never of p.snapshot: the bridge asks the moment
		// a stream ends, and the snapshot can be a whole poll behind that moment.
		SessionListed: func(lctx context.Context, session string) (bool, error) {
			sessions, err := dc.ListSessions(lctx)
			if err != nil {
				return false, err
			}
			return listedAlive(sessions, session), nil
		},
		// New with every process and kept nowhere else: the page asks for it each time
		// it opens a terminal, so a restart — which ends every open terminal anyway —
		// is all it takes to replace it. rand.Text carries at least 128 random bits.
		TerminalToken: rand.Text(),

		SetCardField: setCardField,
		CreateCard:   create,
		// Without this the server has nothing to confine a card write to and answers
		// every one of them 503 — deliberately, since the path arrives from the
		// browser and internal/board will rewrite a frontmatter line in any file
		// that has one.
		BoardDir: cfg.BoardPath,

		// The other end of the docs.paths configuration key: the directories the
		// operator listed are what the documentation section reads, and the only
		// directories a document request may resolve into. Left unwired, the key
		// would be parsed, validated and read by nobody.
		DocsRoots: cfg.DocsPaths,

		// Deliberately outside any repository the operator works in, and not
		// derived from a session's own working directory: a file written into a
		// working tree survives the conversation that produced it and eventually
		// reaches somebody's commit. The cost of keeping it out is one permission
		// prompt the first time a session reads from here, which the operator
		// answers from the panel — see internal/server/image.go.
		ImageDir: imagesDir(),

		// This is the other end of cmd/fleetdeck-status: the reporter posts to
		// /api/status, the server hands it here, and Collect prefers it over the
		// transcript estimate.
		PutStatus: collector.PutStatus,

		Digest: func(sessionID string, limit int) ([]transcript.Step, error) {
			path, err := transcript.Locate(projectsDir(), sessionID)
			if err != nil {
				return nil, err
			}
			return transcript.Digest(path, limit)
		},
		SetOrchestratorSession: func(id string) error {
			return setOrchestratorSession(configPath, collector, id)
		},
		SetSessionLabel: func(sessionID, label string) error {
			return setSessionLabel(configPath, collector, sessionID, label)
		},

		// Which build this is: stamped on every snapshot, and its web hash put
		// into the page itself, so a page left open while this binary is
		// replaced can tell that it is out of date.
		Build: readBuild(),
	}
}

// readBuild fingerprints the running binary. A panel that cannot hash its own
// embedded interface -- which would be a build mistake, not a runtime one --
// still starts; it only loses the ability to tell an open page it is stale.
func readBuild() *buildinfo.Fingerprint {
	f, err := buildinfo.Read(web.FS)
	if err != nil {
		log.Printf("fleetdeck: no build fingerprint, pages will not be told when they are out of date: %v", err)
		return nil
	}
	return &f
}
