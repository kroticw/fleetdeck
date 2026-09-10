// Command fleetdeck serves the fleet control panel on the loopback interface.
//
// This is where every internal/ package meets: the daemon client, the board reader
// and writer, the transcript reader, the usage fetcher, the snapshot and its
// notification rules, the notifier, and the HTTP and WebSocket surface. None of them
// knows about any of the others; the wiring lives here and nowhere else.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/notify"
	"github.com/kroticw/fleetdeck/internal/server"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
	"github.com/kroticw/fleetdeck/internal/version"
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

	configPath := flag.String("config", defaultConfigPath(), "path to the configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		runVersion(os.Stdout)
		return
	}

	if err := run(*configPath); err != nil {
		log.Fatalf("fleetdeck: %v", err)
	}
}

// defaultConfigPath is where spec section 11 puts the configuration file. A missing
// file is a set of defaults, not a failure, so this default is usable on a machine
// that has never been configured.
func defaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "fleetdeck", "config.yaml")
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
// that cannot be used to find out which source is down.
func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Discover rather than a resolved socket path: the daemon's socket directory
	// name is not stable across restarts, and a client bound to today's path keeps
	// failing after the daemon comes back under a new one (spec section 7).
	dc := daemon.Discover(daemon.ControlKey)
	uf := usage.NewFetcher(usage.KeychainToken, usage.Endpoint, usageTTL)
	collector := NewCollector(cfg, dc, uf, projectsDir())

	p := newPanel(
		collector.Collect,
		notify.New(notify.OSAScriptSend),
		cfg.Notify,
		func(err error) { log.Printf("notify: %v", err) },
	)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		poll(ctx, cfg.DaemonPollInterval, p.refresh)
	}()
	go func() {
		defer wg.Done()
		watchBoard(ctx, cfg.BoardPath, func() { p.refresh(ctx) })
	}()

	// Bind before announcing anything: a bare fmt.Sprintf("127.0.0.1:%d", ...)
	// printed ahead of ListenAndServe made a failed start look like a running
	// panel — the log carried the success line and then an unrelated-looking
	// bind error, right next to whichever process actually holds the port
	// (most often this same panel, already started by the launchd agent
	// fleetdeck init installs). ln.Addr() is used for the log line rather
	// than the address that was asked for so that server.port: 0 — "let the
	// OS choose" — reports the port it actually got, not literally "0".
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.ServerPort))
	if err != nil {
		stop()
		wg.Wait()
		return fmt.Errorf("bind 127.0.0.1:%d: %w (a panel may already be running there)", cfg.ServerPort, err)
	}

	srv := &http.Server{
		Handler:           server.New(deps(ctx, p, dc, collector, cfg, configPath)),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("fleetdeck %s listening on http://%s", version.String(), ln.Addr())
		serveErr <- srv.Serve(ln)
	}()

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// deps is the whole contract between this program and the HTTP surface. Every entry
// is a function internal/server calls and none of them reaches back here.
func deps(ctx context.Context, p *panel, dc *daemon.Client, collector *Collector, cfg config.Config, configPath string) server.Deps {
	return server.Deps{
		Snapshot:   p.snapshot,
		SendText:   func(session, text string) error { return dc.SendText(ctx, session, text) },
		SendKeys:   func(session, keys string) error { return dc.SendKeys(ctx, session, keys) },
		ReadScreen: func(session string, tail int) daemon.ScreenResult { return dc.ReadScreen(ctx, session, tail) },

		SetCardField: setCardField,
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
			next := collector.Config()
			next.OrchestratorSession = id
			if err := config.Save(configPath, next); err != nil {
				return err
			}
			collector.SetOrchestratorSession(id)
			return nil
		},
	}
}
