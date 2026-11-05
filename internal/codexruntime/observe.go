package codexruntime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

// observer keeps the latest thread status Codex reported and when. Without a
// live connection nothing is known: activity and NeedsHuman become unknown.
type observer struct {
	mu        sync.Mutex
	connected bool
	status    string
	flags     []string
	at        time.Time
}

type threadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

// handle runs on the native reader; dispatch has already dropped other
// threads' events, including a subagent's.
func (o *observer) handle(m nativeMessage) {
	if m.Method != "thread/status/changed" {
		return
	}
	var p struct {
		Status threadStatus `json:"status"`
	}
	if json.Unmarshal(m.Params, &p) == nil && p.Status.Type != "" {
		o.set(p.Status)
	}
}

func (o *observer) set(s threadStatus) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.connected, o.status, o.flags, o.at = true, s.Type, s.ActiveFlags, time.Now().UTC()
}

func (o *observer) lost() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.connected = false
}

// view reports activity, whether a human is needed, and the observation
// time. pending counts requests the runtime holds unanswered.
func (o *observer) view(pending int) (string, executor.Verdict, time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.connected || o.status == "" {
		return "unknown", executor.Unknown, o.at
	}
	activity := map[string]string{"active": "working", "idle": "idle", "notLoaded": "idle", "systemError": "error"}[o.status]
	if activity == "" {
		activity = "unknown"
	}
	needs := executor.No
	if pending > 0 || len(o.flags) > 0 {
		needs = executor.Yes
	}
	return activity, needs, o.at
}

// nativeLink keeps one control connection for the owned thread, replacing
// a lost one. Resuming on the new connection replays pending requests under
// fresh tokens; the old tokens die with the old connection.
type nativeLink struct {
	mu      sync.Mutex
	current *nativeControl
	socket  string
	thread  string
	obs     *observer
}

func (l *nativeLink) get() (*nativeControl, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current == nil || isDone(l.current.done) {
		return nil, executor.ErrUnavailable
	}
	return l.current, nil
}

// reconcile reads the thread status after (re)connecting rather than waiting
// for the next event.
func (l *nativeLink) reconcile(ctx context.Context, c *nativeControl) error {
	var read struct {
		Thread struct {
			Status threadStatus `json:"status"`
		} `json:"thread"`
	}
	if err := c.call(ctx, "thread/read", map[string]any{"threadId": l.thread, "includeTurns": false}, &read); err != nil {
		return err
	}
	l.obs.set(read.Thread.Status)
	return nil
}

func (l *nativeLink) connect(ctx context.Context) error {
	c, err := openNativeControl(ctx, l.socket, l.obs.handle)
	if err != nil {
		return err
	}
	c.own(l.thread)
	if err := c.call(ctx, "thread/resume", map[string]any{"threadId": l.thread, "excludeTurns": true}, nil); err != nil {
		c.Close()
		return err
	}
	if err := l.reconcile(ctx, c); err != nil {
		c.Close()
		return err
	}
	l.mu.Lock()
	l.current = c
	l.mu.Unlock()
	return nil
}

// maintain reconnects until ctx ends, backing off up to 30 s.
func (l *nativeLink) maintain(ctx context.Context) {
	for {
		l.mu.Lock()
		current := l.current
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-current.done:
		}
		l.obs.lost()
		for delay := 250 * time.Millisecond; ; delay = min(delay*2, 30*time.Second) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			if l.connect(ctx) == nil {
				break
			}
		}
	}
}

func (l *nativeLink) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current != nil {
		l.current.Close()
	}
}
