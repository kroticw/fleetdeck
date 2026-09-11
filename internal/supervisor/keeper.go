package supervisor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
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
)

func (s State) String() string {
	switch s {
	case Answering:
		return "answering"
	case Starting:
		return "starting"
	case Failed:
		return "failed"
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
// Whatever already answers at URL is used as it is -- a panel from a launch
// agent, from a terminal, from a window that has since quit -- and watched
// until it goes, at which point the keeper starts its own. The keeper's own
// panel is started again when it dies, unless it dies within MinUptime of
// starting: then the keeper stops, reports Failed with the end of the log,
// and waits for Retry. launchd does the same thing with its ThrottleInterval,
// for the same reason: a panel that dies at once dies at once again.
//
// When Run returns, the keeper's panel keeps running. The panel outlives the
// window: notifications keep coming, the status line keeps finding where to
// report.
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
			k.emit(Event{State: Answering})
			k.waitUntilGone(ctx)
			continue
		}
		if k.runOwn(ctx) {
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
	// A Retry asked for before this failure is not an answer to it.
	select {
	case <-k.retry:
	default:
	}
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
