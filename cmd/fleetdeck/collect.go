package main

import (
	"context"
	"maps"
	"math"
	"os"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/transcript"
	"github.com/kroticw/fleetdeck/internal/usage"
)

// reportTTL is how long a statusline report is preferred over the transcript
// estimate before it expires.
//
// The reporter runs whenever Claude Code redraws a session's status line, so a
// working session produces a fresh report continuously. A report that has stopped
// arriving therefore means the reporter has stopped running — uninstalled, crashed,
// or a session that ended — and its last value is frozen while the transcript
// estimate is still moving. Showing that frozen number forever, marked as exact, is
// worse than an honest estimate, so a report older than this is dropped and the
// estimate takes over.
//
// The cost of the expiry is a genuinely idle session whose exact number was still
// correct being downgraded to an estimate. The panel marks it as one, which is true,
// and five minutes is long enough that no working session ever crosses it.
const reportTTL = 5 * time.Minute

// reportedPercentWindow is the denominator a statusline report is recorded against.
//
// The reporter measures an occupancy percentage, not a token count: Claude Code hands
// it context_window.used_percentage and nothing that would let it name the tokens
// behind that number (spec section 3.2). Recording it as a percentage out of 100 says
// exactly that and no more — the ratio the panel draws is exact, and the fields carry
// no invented token count. transcript.Usage.Estimated is what tells the two shapes
// apart: false is a reported percentage out of 100, true is a real token count out of
// the model's real window.
const reportedPercentWindow = 100

// usageTimeout bounds the one source that leaves this machine.
//
// Every other source Collect asks is local and bounded by its own package: the daemon
// client carries its own dials and deadlines, internal/board bounds every git
// invocation. The usage endpoint is an unofficial HTTP handle behind a beta header
// (spec section 3.2) reached with no client timeout of its own, so without a deadline
// here one slow response freezes the whole collect cycle and the panel stops showing
// sessions because a rate-limit gauge is slow. A failure is cached by nothing, so the
// next tick simply tries again.
//
// It bounds the request, not the Keychain lookup that precedes it: reading the token
// shells out to `security` with no context, which this cannot reach into.
//
// A var rather than a const solely so a test can shorten it instead of waiting the
// real deadline out; nothing outside a test may write to it.
var usageTimeout = 10 * time.Second

// cachedUsage remembers the last context estimate together with the file state it was
// computed from, so an idle session costs no reads at all. Transcripts reach tens of
// megabytes and Collect runs every couple of seconds; without this the panel would
// read hundreds of megabytes a minute to learn nothing.
type cachedUsage struct {
	usage transcript.Usage
	size  int64
	mtime time.Time
}

// reported is one statusline report, kept with the moment it arrived so a report from
// a reporter that has since stopped running can be told from a current one.
type reported struct {
	model          string
	contextPercent float64
	costUSD        float64
	at             time.Time
}

// Collector asks every source in turn. A source that fails fills its own error field;
// the others are unaffected. This is where spec section 7's degrade-in-parts rule
// becomes code.
type Collector struct {
	cfg   config.Config
	cfgMu sync.RWMutex // guards cfg; everything else in Collector has its own mutex already

	daemon      *daemon.Client
	usage       *usage.Fetcher
	projectsDir string

	// cacheMu guards contextCache alone, and reportMu guards reports alone. They are
	// two mutexes rather than one because they are contended by different callers:
	// reports arrive on HTTP handler goroutines while a collect cycle may be part way
	// through reading a transcript from disk, and one lock would make the reporter
	// wait on that read.
	cacheMu      sync.Mutex
	contextCache map[string]cachedUsage

	reportMu sync.Mutex
	reports  map[string]reported

	// now is time.Now everywhere but in tests, which cannot wait out reportTTL.
	now func() time.Time
}

// NewCollector builds a collector over the sources the panel was wired with. Any of
// dc and uf may be nil: a panel that could not reach a daemon, or was configured
// without the usage endpoint, still collects everything else.
func NewCollector(cfg config.Config, dc *daemon.Client, uf *usage.Fetcher, projectsDir string) *Collector {
	return &Collector{
		cfg:          cfg,
		daemon:       dc,
		usage:        uf,
		projectsDir:  projectsDir,
		contextCache: map[string]cachedUsage{},
		reports:      map[string]reported{},
		now:          time.Now,
	}
}

// Config returns a copy of the collector's current configuration. Safe for
// concurrent use with SetOrchestratorSession and SetSessionLabel.
//
// Every field of config.Config used to be a value type, which made "return
// c.cfg" under RLock a genuinely independent snapshot: copying the struct
// copied all of it. SessionLabels broke that silently — a map is a
// reference type, so copying the struct only copies the map header, and the
// copy still points at the very map SetSessionLabel mutates. Caught by
// go test -race: a goroutine reading cfg.SessionLabels from a Config() the
// poll loop's Collect() had just fetched, racing a goroutine calling
// SetSessionLabel, is a "concurrent map read and map write" fatal error
// outside of -race, not a panic anything can recover from. Cloning the map
// here is what makes the rest of this type's "everything is a value copy"
// contract true again.
func (c *Collector) Config() config.Config {
	c.cfgMu.RLock()
	defer c.cfgMu.RUnlock()
	cfg := c.cfg
	cfg.SessionLabels = maps.Clone(cfg.SessionLabels)
	return cfg
}

// SetOrchestratorSession updates the pinned orchestrator session id kept in
// memory. The caller must persist it to disk first (config.Save) — this only
// updates what Collect() reports next, so a failed save never leaves memory
// ahead of the config file.
func (c *Collector) SetOrchestratorSession(id string) {
	c.cfgMu.Lock()
	c.cfg.OrchestratorSession = id
	c.cfgMu.Unlock()
}

// SetSessionLabel updates one entry of the in-memory session_labels map kept
// alongside the rest of the collector's configuration — the same "caller
// persists to disk first, this only updates what Collect() reports next"
// division of labor as SetOrchestratorSession, so a failed config.Save never
// leaves memory ahead of the file on disk. An empty label deletes the
// in-memory entry entirely rather than storing an empty string, matching
// internal/config.SetSessionLabel's own contract for the file itself: the
// two must never disagree about whether a session has a label.
func (c *Collector) SetSessionLabel(sessionID, label string) {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	if label == "" {
		delete(c.cfg.SessionLabels, sessionID)
		return
	}
	if c.cfg.SessionLabels == nil {
		c.cfg.SessionLabels = map[string]string{}
	}
	c.cfg.SessionLabels[sessionID] = label
}

// PutStatus records what cmd/fleetdeck-status posted. It is server.Deps.PutStatus,
// field for field, and it is the only writer of the report store.
//
// Reports are keyed by sessionID, which is the transcript UUID
// (daemon.Session.SessionID): the reporter gets its id from Claude Code, which knows
// nothing of the daemon's short ids, so a store keyed on Short would never match a
// single report.
//
// A percentage outside 0..100 is clamped rather than stored as given. It cannot be
// drawn and it cannot be true, and clamping keeps one malformed report from producing
// a bar that runs off its track.
func (c *Collector) PutStatus(sessionID, model string, contextPercent, costUSD float64) {
	if sessionID == "" {
		return
	}
	c.reportMu.Lock()
	defer c.reportMu.Unlock()
	c.reports[sessionID] = reported{
		model:          model,
		contextPercent: math.Min(math.Max(contextPercent, 0), 100),
		costUSD:        costUSD,
		at:             c.now(),
	}
}

// reportFor returns the live report for a session, if it has one. An expired report
// is deleted on the way out rather than merely ignored, so the store does not grow a
// permanent entry for every session that ever ran.
func (c *Collector) reportFor(sessionID string) (reported, bool) {
	if sessionID == "" {
		return reported{}, false
	}
	c.reportMu.Lock()
	defer c.reportMu.Unlock()
	r, ok := c.reports[sessionID]
	if !ok {
		return reported{}, false
	}
	if c.now().Sub(r.at) > reportTTL {
		delete(c.reports, sessionID)
		return reported{}, false
	}
	return r, true
}

// transcriptState returns everything one stat of a session's transcript tells the
// panel: the context estimate, whether there is one, and how long the session has
// been silent.
//
// Both answers come from the same os.Stat deliberately. Silence is the age of the
// last write to the transcript (spec section 6) and the estimate cache is keyed on
// size and mtime, so statting twice would be the same syscall run twice and could
// disagree with itself in between.
//
// A transcript that cannot be stat'ed at all returns a zero duration. Per spec
// section 6 that reads as "not measured", never as "silent forever": state.Diff does
// not fire the silence rule on a zero, which is what keeps a session whose transcript
// does not exist yet from being reported as half an hour silent in its first second.
func (c *Collector) transcriptState(path string) (transcript.Usage, bool, time.Duration) {
	fi, err := os.Stat(path)
	if err != nil {
		return transcript.Usage{}, false, 0
	}

	silentFor := c.now().Sub(fi.ModTime())
	if silentFor < 0 {
		// A transcript stamped in the future (a clock adjustment, a copied file) has
		// not been silent for a negative time. Zero is the honest answer: not
		// measured.
		silentFor = 0
	}

	c.cacheMu.Lock()
	hit, ok := c.contextCache[path]
	c.cacheMu.Unlock()
	if ok && hit.size == fi.Size() && hit.mtime.Equal(fi.ModTime()) {
		return hit.usage, true, silentFor
	}

	u, err := transcript.ContextUsage(path)
	if err != nil {
		return transcript.Usage{}, false, silentFor
	}

	c.cacheMu.Lock()
	c.contextCache[path] = cachedUsage{usage: u, size: fi.Size(), mtime: fi.ModTime()}
	c.cacheMu.Unlock()
	return u, true, silentFor
}

// enrich fills the two fields state.Link cannot: the context reading and the silence
// measurement. Both need I/O, which internal/state performs none of. It returns the
// set of transcript paths it visited, which is exactly the set of live transcripts
// the context cache may keep.
//
// Where a session has both a live statusline report and a transcript estimate, the
// report wins: it comes from Claude Code itself and is exact, and the estimate is the
// fallback for when the reporter is not installed (spec section 3.2). The estimate is
// still computed, because it is what the session falls back to the moment the report
// expires — and because the same stat is what measures silence either way.
//
// The report also carries the model name and the running cost, which have no fallback
// at all: nothing outside a session can obtain either, which is why cmd/fleetdeck-status
// exists. A session with no live report keeps an empty model and a nil cost, and the
// panel shows neither rather than inventing one.
// labels is the configuration file's session_labels map, keyed by the same
// transcript UUID as views[i].SessionID. It is read fresh from cfg on every
// call rather than cached on the Collector: a label an operator just wrote
// through the session-label route must show up on the very next Collect(),
// and there is nothing else Collector already mirrors in memory the way
// SetOrchestratorSession does for the pinned orchestrator id. A session
// whose id has no entry here simply gets no label — labels are never pruned
// when a session disappears from the daemon's list (see
// internal/config.SetSessionLabel's own comment), so a stale entry for a
// dead session is expected and harmless: this loop only ever assigns a
// label to a view that is already in the daemon's live list.
func (c *Collector) enrich(views []state.SessionView, labels map[string]string) map[string]struct{} {
	live := map[string]struct{}{}
	for i := range views {
		id := views[i].SessionID
		if id == "" {
			continue
		}
		views[i].Label = labels[id]
		if path, err := transcript.Locate(c.projectsDir, id); err == nil {
			live[path] = struct{}{}
			estimate, haveEstimate, silentFor := c.transcriptState(path)
			views[i].SilentFor = silentFor
			if haveEstimate {
				u := estimate
				views[i].Context = &u
			}
		}
		if r, ok := c.reportFor(id); ok {
			views[i].Context = &transcript.Usage{
				Tokens:    int(math.Round(r.contextPercent)),
				Window:    reportedPercentWindow,
				Estimated: false,
			}
			// The model name and the cost have no fallback the way the context
			// does: Claude Code hands both to its statusline command and to
			// nothing else, so they are set here or they are never set at all.
			// The cost is taken by address rather than by value because zero is a
			// cost a session genuinely can have, and the panel must be able to
			// tell that from a session nobody reported on.
			views[i].Model = r.model
			cost := r.costUSD
			views[i].CostUSD = &cost
		}
	}
	return live
}

// pruneContextCache forgets the estimate of every transcript that is no longer behind
// a live session. Without it the cache keeps one entry per session the panel has ever
// seen, for as long as the process runs.
func (c *Collector) pruneContextCache(live map[string]struct{}) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	for path := range c.contextCache {
		if _, ok := live[path]; !ok {
			delete(c.contextCache, path)
		}
	}
}

// Collect asks each source separately. A failure fills that source's own error field
// in the snapshot and leaves every other field alone, so a dead daemon still returns
// a full board and an unreadable board still returns a full session list.
func (c *Collector) Collect(ctx context.Context) state.Snapshot {
	cfg := c.Config()
	snap := state.Snapshot{At: c.now()}
	snap.OrchestratorSession = cfg.OrchestratorSession

	var sessions []daemon.Session
	if c.daemon == nil {
		// A panel built without a daemon client is a configuration fact, not a
		// crash. It reports the same way an unreachable daemon does.
		snap.DaemonError = daemon.ErrDaemonUnavailable.Error()
	} else {
		var err error
		sessions, err = c.daemon.ListSessions(ctx)
		if err != nil {
			snap.DaemonError = err.Error()
			// ListSessions returns nothing usable alongside an error; make sure a
			// partial slice can never be linked against.
			sessions = nil
		}
	}

	var cards []board.Card
	if cfg.BoardPath != "" {
		var err error
		cards, err = board.Scan(cfg.BoardPath)
		if err != nil {
			snap.BoardError = err.Error()
			cards = nil
		}
	}

	snap.Cards = cards
	snap.Sessions = state.Link(sessions, cards)
	snap.OrphanCards = state.OrphanCards(sessions, cards)
	c.pruneContextCache(c.enrich(snap.Sessions, cfg.SessionLabels))

	if cfg.UsageEnabled && c.usage != nil {
		usageCtx, cancel := context.WithTimeout(ctx, usageTimeout)
		l, err := c.usage.Limits(usageCtx)
		cancel()
		if err != nil {
			snap.UsageError = err.Error()
		} else {
			snap.Limits = &l
		}
	}
	return snap
}
