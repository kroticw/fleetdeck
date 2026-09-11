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
	"strings"
	"sync"
	"syscall"
	"time"
)

// State is what a Keeper reports about the panel at its URL.
type State int

const (
	// Answering: something answers at the URL. Event.Ours says whether it is
	// the panel this keeper started.
	Answering State = iota + 1
	// Starting: nothing answered, and the keeper has started its panel.
	Starting
	// Failed: the keeper's panel could not be started, died before it
	// answered, never answered, or died too soon after it started. The keeper
	// does nothing more until Retry.
	Failed
	// Replacing: a panel a window left behind answers at the URL; the keeper
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
}

// stopGrace is how long a panel the keeper gives up on has to leave after
// SIGTERM: the panel's own graceful shutdown bound (shutdownTimeout in
// cmd/fleetdeck, 5 s) and one second more.
const stopGrace = 6 * time.Second

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
	// MinUptime: a panel that dies sooner than this after it started is not
	// started again without Retry.
	MinUptime time.Duration
	// Poll is how often a panel the keeper did not start is checked.
	Poll time.Duration

	// Owner is the PID of the window this keeper runs in, which its panels are
	// started to report as their owner (in Args). Zero for a keeper that is not
	// a window's: it replaces nothing.
	Owner int

	// OnEvent is called from Run's goroutine.
	OnEvent func(Event)

	once  sync.Once
	retry chan struct{}
}

func (k *Keeper) init() {
	k.once.Do(func() { k.retry = make(chan struct{}, 1) })
}

// Retry asks a keeper that has reported Failed to try again.
func (k *Keeper) Retry() {
	k.init()
	select {
	case k.retry <- struct{}{}:
	default:
	}
}

// Run keeps a panel answering at URL until ctx ends.
func (k *Keeper) Run(ctx context.Context) {
	k.init()
	for ctx.Err() == nil {
		if answers(ctx, k.URL) {
			why, replace := k.replaceable(ctx)
			if !replace {
				k.emit(Event{State: Answering})
				k.waitUntilGone(ctx)
				continue
			}
			k.emit(Event{State: Replacing, Detail: why})
			err := StopHolder(ctx, k.URL, stopGrace)
			if err == nil {
				continue
			}
			k.fail(fmt.Errorf("%s, and it could not be stopped: %w", why, err), "")
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

// waitUntilGone returns when the panel at URL has failed to answer twice in a
// row: one missed answer is a panel busy for a moment, not a panel gone.
func (k *Keeper) waitUntilGone(ctx context.Context) {
	poll := k.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for misses := 0; misses < 2; {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if answers(ctx, k.URL) {
			misses = 0
		} else {
			misses++
		}
	}
}

// runOwn starts the keeper's panel and stays with it until it exits. It
// reports false when the keeper should stop and wait for Retry.
func (k *Keeper) runOwn(ctx context.Context) bool {
	p, err := StartPanel(k.Bin, k.Args, k.Env, k.LogPath)
	if err != nil {
		k.fail(err, "")
		return false
	}
	started := time.Now()
	k.emit(Event{State: Starting, PID: p.PID})

	answerCtx, cancel := context.WithTimeout(ctx, k.StartTimeout)
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
			_ = p.Stop(stopGrace)
			k.fail(fmt.Errorf("the panel started (pid %d), but nothing answered at %s within %s; it has been stopped", p.PID, k.URL, k.StartTimeout),
				LogTail(k.LogPath, tailLines))
			return false
		}
	}

	k.emit(Event{State: Answering, Ours: true, PID: p.PID})
	select {
	case <-ctx.Done():
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

// answers reports whether anything answers HTTP at url right now, whatever
// the status.
func answers(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: answerTimeout}).Do(req)
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

// replaceable says whether the panel answering at URL is one a window left
// behind, and whose it was.
func (k *Keeper) replaceable(ctx context.Context) (string, bool) {
	if k.Owner == 0 {
		return "", false
	}
	b, ok := holderBuild(ctx, k.URL)
	if !ok {
		return "", false // not a fleetdeck panel: not this window's to decide about
	}
	switch {
	case b.Owner != 0:
		// This window's own panel is left alone by the same test: its window,
		// this process, is there. A window that exits takes its panel with it
		// within moments; a panel still answering is one shutting down, or one
		// whose window died in a way its watch could not see. kill -0 is fine
		// here: a PID taken by some other process since only means a panel
		// that is going anyway is used for those moments.
		if errors.Is(syscall.Kill(b.Owner, 0), syscall.ESRCH) {
			return fmt.Sprintf("the panel at %s belongs to a window (pid %d) that is gone", k.URL, b.Owner), true
		}
		return "", false
	case strings.Contains(b.Executable, ".app/Contents/MacOS/"):
		// TEMPORARY, for the change-over only. A panel started by a window from
		// before panels reported their owner runs from inside an app bundle and
		// reports none -- the case that showed an old build to a new window on
		// 2026-09-11. Remove this case once no machine can have such a panel
		// running: when every fleetdeck panel answering anywhere reports its
		// owner. Left in past that, it would stop a debugging run started from
		// inside a bundle by hand.
		return fmt.Sprintf("the panel at %s runs from an app bundle (%s) but reports no window: it was left by a window from before panels reported their owner", k.URL, b.Executable), true
	default:
		return "", false // started some other way: somebody's on purpose
	}
}

// holderInfo is what a panel's build fingerprint says about which panel it
// is.
type holderInfo struct {
	Owner      int    `json:"owner"`
	Executable string `json:"executable"`
}

// holderBuild is what the panel at panelURL says about itself, or false when
// what answers is not a fleetdeck panel: no build fingerprint in a snapshot.
func holderBuild(ctx context.Context, panelURL string) (holderInfo, bool) {
	u, err := url.Parse(panelURL)
	if err != nil {
		return holderInfo{}, false
	}
	u.Path = "/api/snapshot"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return holderInfo{}, false
	}
	resp, err := (&http.Client{Timeout: answerTimeout}).Do(req)
	if err != nil {
		return holderInfo{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	var snap struct {
		Build *holderInfo `json:"build"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&snap) != nil || snap.Build == nil {
		return holderInfo{}, false
	}
	return *snap.Build, true
}
