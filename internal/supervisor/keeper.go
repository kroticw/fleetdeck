package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"syscall"
	"time"
)

// State is what a Keeper reports about the panel at its URL.
type State int

const (
	// Answering: something answers at the URL. Event.Ours says whether it is
	// the panel this keeper started; Event.Holder, for one it did not, what
	// that panel says of its build.
	Answering State = iota + 1
	// Starting: nothing answered, and the keeper has started its panel.
	Starting
	// Failed: the keeper's panel could not be started, died before it
	// answered, never answered, or died too soon after it started. The keeper
	// does nothing more until Retry.
	Failed
	// Replacing: a panel a window left behind answers at the URL, or a person
	// asked for the panel answering there to be replaced (Replace); the keeper
	// is stopping it to start its own. Event.Detail says whose it was.
	Replacing
)

func (s State) String() string {
	switch s {
	case Answering:
		return "answering"
	case Starting:
		return "starting"
	case Failed:
		return "failed"
	case Replacing:
		return "replacing"
	}
	return fmt.Sprintf("State(%d)", int(s))
}

// Event is one change a Keeper reports.
type Event struct {
	State State
	// Ours: the panel answering is the one this keeper started.
	Ours bool
	// PID of the panel this keeper started; 0 for a panel it did not.
	PID int
	// Err says why, for Failed.
	Err error
	// LogTail is the end of the panel's log, for Failed: what the panel itself
	// said before it went.
	LogTail string
	// Detail says whose panel is being replaced, for Replacing.
	Detail string
	// Holder is what a panel the keeper did not start says of its build, for
	// Answering; nil when what answers is not a fleetdeck panel. It is
	// reported again whenever another build takes the port.
	Holder *PanelBuild
	// Asked: a person asked for this replacement (Replace), for Replacing.
	Asked bool
	// Took is how long the panel this keeper started took to answer, from its
	// start, for Answering with Ours; 0 otherwise.
	Took time.Duration
}

// PanelBuild is what a fleetdeck panel says of its build in its snapshot
// (internal/buildinfo's Fingerprint): as much of it as tells one build, and
// one panel, from another.
type PanelBuild struct {
	Version    string `json:"version"`
	Revision   string `json:"revision"`
	Modified   bool   `json:"modified"`
	Executable string `json:"executable"`
	Owner      int    `json:"owner"`
	// PID is not the panel's word but the kernel's: the process listening on
	// the port when the keeper looked. 0 when it could not be found.
	PID int `json:"-"`
}

// stopGrace is how long a panel the keeper gives up on has to leave after
// SIGTERM: the panel's own graceful shutdown bound (shutdownTimeout in
// cmd/fleetdeck, 5 s) and one second more.
const stopGrace = 6 * time.Second

// handoverStopGrace is how long the panel a restart replaces is given to go on
// SIGTERM before it is killed. Restart is an update's: the panel it replaces is
// the one the new window started from the staging directory a moment earlier,
// and the old window gives the whole handover 2.436 s, a deadline written into
// the version already installed that no new version can change (T-060). A
// panel told to stop cancels its collect cycle and goes -- 2-5 ms after SIGTERM,
// measured on a stand with a HOME at the operator's scale -- so 300 ms is far
// more than it needs, and leaves the handover most of its deadline. SIGKILL is
// safe there: a panel writes nothing without a request, and every write a
// request makes is whole or not at all (docs/engineering/window-and-panel.md,
// "Stopping the staged panel").
const handoverStopGrace = 300 * time.Millisecond

// Keeper makes sure a panel answers at URL, and when none does, starts the
// panel at Bin. This is what the launch agent did before the window took the
// panel over: it started the panel, and launchd's KeepAlive started it again
// when it died.
//
// A panel lives exactly as long as the window that started it (the
// operator's rule, 2026-09-11; the panel keeps it itself, see cmd/fleetdeck's
// owner.go). So a panel found answering at URL is either a window's -- a
// window still running, or one a window left behind -- or one started some
// other way, from a terminal. A keeper that knows its window (Owner) replaces
// a panel whose window is gone, and uses any other as it is, watching it
// until it goes and then starting its own: a panel from a terminal is
// somebody's on purpose, and neither a panel of a live window nor anything
// that is not a fleetdeck panel is this window's to decide about.
//
// Used as it is, but not in silence: the keeper reports what such a panel
// says of its build (Event.Holder), so the window can say when it is not the
// window's own -- on 2026-09-14 a v0.9.0 window showed a panel built three
// days earlier, kept on the port by a launch agent, and nothing said so. And a
// person who sees that may have the keeper replace it (Replace).
//
// The keeper's own panel is started again when it dies, unless it dies within
// MinUptime of starting: then the keeper stops, reports Failed with the end of
// the log, and waits for Retry. launchd does the same thing with its
// ThrottleInterval, for the same reason: a panel that dies at once dies at
// once again.
//
// When Run returns, the keeper's panel is not stopped by the keeper: the panel
// goes by itself when its window -- the process that runs the keeper -- goes.
type Keeper struct {
	URL     string
	Bin     string
	Args    []string
	Env     []string
	LogPath string

	// StartTimeout is how long a started panel has to answer at URL.
	StartTimeout time.Duration
	// StartLimitsNow, when set, is asked at each start in place of
	// StartTimeout and stopGrace: during an update, what the takeover has left
	// of the old window's deadline (Takeover.StartLimits).
	StartLimitsNow func() StartLimits
	// MinUptime: a panel that dies sooner than this after it started is not
	// started again without Retry.
	MinUptime time.Duration
	// Poll is how often a panel the keeper did not start is checked.
	Poll time.Duration

	// Owner is the PID of the window this keeper runs in, which its panels are
	// started to report as their owner (in Args). Zero for a keeper that is not
	// a window's: it replaces nothing by itself.
	Owner int

	// MayReplace, when set, is asked at a press whether the panel on the port
	// by then may be replaced at a person's request: the window knows what the
	// keeper does not, such as a launch agent that would start it again.
	MayReplace func(PanelBuild) bool

	// OnEvent is called from Run's goroutine.
	OnEvent func(Event)

	once      sync.Once
	retry     chan struct{}
	replace   chan PanelBuild
	restartTo chan string
	binMu     sync.Mutex
}

func (k *Keeper) init() {
	k.once.Do(func() {
		k.retry = make(chan struct{}, 1)
		k.replace = make(chan PanelBuild, 1)
		k.restartTo = make(chan string, 1)
	})
}

// Restart asks the keeper to stop its panel and start it again from bin. It
// takes effect while the keeper's own panel answers, and is not counted as
// the panel dying.
//
// Its one caller is an update, restarting the panel from the canonical path
// once the new bundle is there. Setting Bin without stopping the panel would
// not do: the panel that runs was started from the staging directory, which
// holds the bundle that was swapped out by then, and three things follow from
// where a panel was started rather than from which build it is -- the path
// the keeper starts the next one from, the path the panel reports in
// /api/snapshot, and the path `fleetdeck init` writes into Claude Code's
// statusLine.command. docs/engineering/window-and-panel.md says it at length.
func (k *Keeper) Restart(bin string) {
	k.init()
	select {
	case <-k.restartTo:
	default:
	}
	k.restartTo <- bin
}

func (k *Keeper) bin() string {
	k.binMu.Lock()
	defer k.binMu.Unlock()
	return k.Bin
}

func (k *Keeper) setBin(bin string) {
	k.binMu.Lock()
	defer k.binMu.Unlock()
	k.Bin = bin
}

// Retry asks a keeper that has reported Failed to try again.
func (k *Keeper) Retry() {
	k.init()
	select {
	case k.retry <- struct{}{}:
	default:
	}
}

// Replace asks the keeper to stop the panel it did not start and is using as
// it is, and to start its own. want is that panel as the person was shown it:
// the build it reported and the PID on the port then (Event.Holder).
//
// Nothing is stopped unless, at the press, the port is held by exactly that
// panel -- the same build, binary and process -- and it may still be replaced:
// it reports no window, and MayReplace agrees. Between the notice and the
// press the port can change hands without going quiet: a second window's panel
// started within the two looks it takes to see a panel gone. And the press
// need not be a person's: the window's bindings are callable from the page,
// and the page is the panel's. A press that does not hold stops nothing, and
// the keeper reports whatever is on the port now. A press made before a panel
// answered is forgotten when one does.
func (k *Keeper) Replace(want PanelBuild) {
	k.init()
	select {
	case <-k.replace:
	default:
	}
	k.replace <- want
}

func (k *Keeper) forgetReplace() {
	select {
	case <-k.replace:
	default:
	}
}

// Run keeps a panel answering at URL until ctx ends.
func (k *Keeper) Run(ctx context.Context) {
	k.init()
	for ctx.Err() == nil {
		if answers(ctx, k.URL) {
			holder, why, replace := k.replaceable(ctx)
			if replace {
				k.emit(Event{State: Replacing, Detail: why})
				err := StopHolder(ctx, k.URL, stopGrace)
				if err == nil {
					continue
				}
				k.fail(fmt.Errorf("%s, and it could not be stopped: %w", why, err), "")
			} else {
				k.withPID(ctx, holder)
				k.forgetReplace()
				k.emit(Event{State: Answering, Holder: holder})
				want, asked := k.watch(ctx, holder)
				if !asked {
					continue
				}
				now, ok := k.pressHolds(ctx, want)
				if !ok {
					continue // whatever is on the port is reported again
				}
				why = fmt.Sprintf("the panel at %s (pid %d, %q), which this window did not start, is replaced at a person's request", k.URL, now.PID, now.Executable)
				k.emit(Event{State: Replacing, Detail: why, Asked: true})
				err := stopListener(ctx, k.URL, now.PID, stopGrace)
				if err == nil || errors.Is(err, errHolderChanged) {
					continue
				}
				k.fail(fmt.Errorf("%s, and it could not be stopped: %w", why, err), "")
			}
		} else if k.runOwn(ctx) {
			continue
		}
		select {
		case <-k.retry:
		case <-ctx.Done():
			return
		}
	}
}

// withPID sets b's PID to the process listening on the port, when it can be
// found.
func (k *Keeper) withPID(ctx context.Context, b *PanelBuild) {
	if b == nil {
		return
	}
	port, err := portOf(k.URL)
	if err != nil {
		return
	}
	if pid, err := listenerPID(ctx, port); err == nil {
		b.PID = pid
	}
}

// watch stays with a panel the keeper did not start, reported as shown, and
// returns when it is no longer what answers at URL, reporting false: gone --
// two missed looks in a row, since one is a panel busy for a moment -- or
// another build on the port, by two looks in a row saying so. It returns
// false when ctx ends too, and the press with true when a person asks for a
// replacement.
func (k *Keeper) watch(ctx context.Context, shown *PanelBuild) (PanelBuild, bool) {
	poll := k.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for misses, changed := 0, 0; misses < 2 && changed < 2; {
		select {
		case <-ctx.Done():
			return PanelBuild{}, false
		case want := <-k.replace:
			return want, true
		case <-tick.C:
		}
		if !answers(ctx, k.URL) {
			misses, changed = misses+1, 0
			continue
		}
		misses = 0
		if b, ok := holderBuild(ctx, k.URL); sameHolder(shown, b, ok) {
			changed = 0
		} else {
			changed++
		}
	}
	return PanelBuild{}, false
}

// sameHolder says whether what the port answers now, b (ok false for not a
// fleetdeck panel), is the build shown was reported with.
func sameHolder(shown *PanelBuild, b PanelBuild, ok bool) bool {
	if shown == nil {
		return !ok
	}
	s := *shown
	s.PID = 0
	return ok && s == b
}

// pressHolds looks at the port again at a press for want, and says whether
// what holds it now may be stopped for it: exactly the panel shown, reporting
// no window, and MayReplace agreeing.
func (k *Keeper) pressHolds(ctx context.Context, want PanelBuild) (PanelBuild, bool) {
	if want.PID == 0 {
		return PanelBuild{}, false
	}
	now, ok := holderBuild(ctx, k.URL)
	if !ok {
		return PanelBuild{}, false
	}
	k.withPID(ctx, &now)
	if now != want || now.Owner != 0 {
		return now, false
	}
	if k.MayReplace != nil && !k.MayReplace(now) {
		return now, false
	}
	return now, true
}

// errHolderChanged: the port is held by another process than the one that was
// to be stopped.
var errHolderChanged = errors.New("another process holds the port now")

// stopListener stops pid, and only while it is what listens on panelURL's
// port: the kernel is asked again before each signal. Done means nothing
// listens there any more; another process there is errHolderChanged, with
// nothing signalled to it.
func stopListener(ctx context.Context, panelURL string, pid int, grace time.Duration) error {
	port, err := portOf(panelURL)
	if err != nil {
		return err
	}
	for _, step := range []struct {
		sig  syscall.Signal
		wait time.Duration
	}{{syscall.SIGTERM, grace}, {syscall.SIGKILL, holderGone}} {
		now, err := listenerPID(ctx, port)
		switch {
		case err != nil:
			return err
		case now == 0:
			return nil
		case now != pid:
			return errHolderChanged
		}
		if err := syscall.Kill(pid, step.sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("signal the panel (pid %d): %w", pid, err)
		}
		if portFreed(ctx, port, step.wait) {
			return nil
		}
	}
	return fmt.Errorf("the panel (pid %d) still holds port %d after SIGKILL", pid, port)
}

// runOwn starts the keeper's panel and stays with it until it exits. It
// reports false when the keeper should stop and wait for Retry.
func (k *Keeper) runOwn(ctx context.Context) bool {
	p, err := StartPanel(k.bin(), k.Args, k.Env, k.LogPath)
	if err != nil {
		k.fail(err, "")
		return false
	}
	started := time.Now()
	k.emit(Event{State: Starting, PID: p.PID})

	limits := StartLimits{Answer: k.StartTimeout, StopGrace: stopGrace}
	if k.StartLimitsNow != nil {
		limits = k.StartLimitsNow()
	}
	answerCtx, cancel := context.WithTimeout(ctx, limits.Answer)
	answered := make(chan error, 1)
	go func() { answered <- WaitAnswer(answerCtx, k.URL) }()
	select {
	case <-p.Exited():
		cancel()
		<-answered
		return k.afterExit(ctx, p, started, false)
	case err := <-answered:
		cancel()
		if ctx.Err() != nil {
			return true
		}
		if err != nil {
			// A panel that runs and does not answer where it is looked for is
			// stopped, not left running where nobody would find it.
			_ = p.Stop(limits.StopGrace)
			k.fail(fmt.Errorf("the panel started (pid %d), but nothing answered at %s within %s; it has been stopped", p.PID, k.URL, limits.Answer),
				LogTail(k.LogPath, tailLines))
			return false
		}
	}

	k.emit(Event{State: Answering, Ours: true, PID: p.PID, Took: time.Since(started)})
	select {
	case <-ctx.Done():
		return true
	case bin := <-k.restartTo:
		k.setBin(bin)
		_ = p.Stop(handoverStopGrace)
		return true
	case <-p.Exited():
		return k.afterExit(ctx, p, started, true)
	}
}

// afterExit decides what the keeper's panel ending means.
func (k *Keeper) afterExit(ctx context.Context, p *Panel, started time.Time, hadAnswered bool) bool {
	// Whoever answers now holds the port: the keeper of a second window that
	// started its panel a moment earlier, or a panel started some other way.
	// That is a panel, not a failure.
	if answers(ctx, k.URL) {
		return true
	}
	if hadAnswered && time.Since(started) >= k.MinUptime {
		return true
	}
	k.fail(fmt.Errorf("the panel (pid %d) ended: %v", p.PID, p.Err()), LogTail(k.LogPath, tailLines))
	return false
}

func (k *Keeper) fail(err error, tail string) {
	k.emit(Event{State: Failed, Err: err, LogTail: tail})
}

func (k *Keeper) emit(e Event) {
	if k.OnEvent != nil {
		k.OnEvent(e)
	}
}

// answerTimeout bounds one look at the panel's URL. A panel on this machine's
// loopback answers in milliseconds; a look that takes longer is a panel that
// is not there to be found.
const answerTimeout = 500 * time.Millisecond

// panelClient is how a window asks a panel anything: whether it answers, what
// build it is, which revision. Keep-alives are off. A pooled connection is one
// the panel may wait on when it stops, and a handover stops the installed panel
// -- the version being replaced, v0.9.2 for the update to v0.9.3, which waits up
// to five seconds on a connection even when it has asked for nothing -- inside
// the old window's 2.436 s for the whole handover (T-060). Without keep-alives
// each connection is closed once its answer is read, and one the transport
// dialed for a probe that another connection served is closed rather than
// kept. Closing idle connections after each probe instead would close other
// callers' connections in the shared transport and miss one a concurrent probe
// is still using. A connection to a panel on this machine's loopback costs
// next to nothing to make again.
var panelClient = &http.Client{
	Timeout:   answerTimeout,
	Transport: &http.Transport{DisableKeepAlives: true},
}

// answers reports whether anything answers HTTP at url right now, whatever
// the status.
func answers(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := panelClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// logTailBytes is how much of the end of a log LogTail reads: many times
// what tailLines lines of a panel's log take, and never the whole of a log
// that has grown for months.
const logTailBytes = 64 << 10

// LogTail is the last n lines of the log at path, or "" when there is none.
func LogTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err == nil && info.Size() > logTailBytes {
		if _, err := f.Seek(-logTailBytes, io.SeekEnd); err != nil {
			return ""
		}
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return ""
	}
	return tail(string(data), n)
}

// replaceable says what the panel answering at URL says of its build -- nil
// when it is not a fleetdeck panel -- and whether it is one a window left
// behind, and whose it was.
func (k *Keeper) replaceable(ctx context.Context) (*PanelBuild, string, bool) {
	b, ok := holderBuild(ctx, k.URL)
	if !ok {
		return nil, "", false // not a fleetdeck panel: not this window's to decide about
	}
	switch {
	case k.Owner == 0:
		return &b, "", false
	case b.Owner != 0:
		// This window's own panel is left alone by the same test: its window,
		// this process, is there. A window that exits takes its panel with it
		// within moments; a panel still answering is one shutting down, or one
		// whose window died in a way its watch could not see. kill -0 is fine
		// here: a PID taken by some other process since only means a panel
		// that is going anyway is used for those moments.
		if errors.Is(syscall.Kill(b.Owner, 0), syscall.ESRCH) {
			return &b, fmt.Sprintf("the panel at %s belongs to a window (pid %d) that is gone", k.URL, b.Owner), true
		}
		return &b, "", false
	default:
		// Started some other way, including from inside an app bundle by hand.
		// Such a panel was once replaced, for the change-over to panels
		// reporting their owner (#108, v0.2.0); on 2026-09-14 that stopped a
		// stand's -stand-socket panel and had the window start one that could
		// reach the real fleet daemon. A panel of another build is named over
		// its page, with a button to replace it (the window's foreign.go).
		return &b, "", false // started some other way: somebody's on purpose
	}
}

// holderBuild is what the panel at panelURL says about itself, or false when
// what answers is not a fleetdeck panel: no build fingerprint in a snapshot.
func holderBuild(ctx context.Context, panelURL string) (PanelBuild, bool) {
	u, err := url.Parse(panelURL)
	if err != nil {
		return PanelBuild{}, false
	}
	u.Path = "/api/snapshot"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return PanelBuild{}, false
	}
	resp, err := panelClient.Do(req)
	if err != nil {
		return PanelBuild{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	var snap struct {
		Build *PanelBuild `json:"build"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&snap) != nil || snap.Build == nil {
		return PanelBuild{}, false
	}
	return *snap.Build, true
}
