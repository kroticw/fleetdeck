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
	context   *ContextUse
	// primary and secondary are the account's limit windows; limitsAt is
	// when either was last reported.
	primary, secondary *LimitWindow
	limitsAt           time.Time
}

// ContextUse is the context the thread's last turn filled, as Codex
// reported it, against the model's window.
type ContextUse struct {
	Tokens     int64     `json:"tokens"`
	Window     int64     `json:"window"`
	ObservedAt time.Time `json:"observedAt"`
}

// AccountLimits are the Codex account's rate-limit windows as last reported,
// in Codex's order (primary first). They are the account's, not the
// thread's, and never mixed with another provider's.
type AccountLimits struct {
	Windows    []LimitWindow `json:"windows"`
	ObservedAt time.Time     `json:"observedAt"`
}

type LimitWindow struct {
	Minutes     int64      `json:"minutes,omitempty"`
	UsedPercent float64    `json:"usedPercent"`
	ResetsAt    *time.Time `json:"resetsAt,omitempty"`
}

type nativeLimitWindow struct {
	UsedPercent float64 `json:"usedPercent"`
	Minutes     *int64  `json:"windowDurationMins"`
	ResetsAt    *int64  `json:"resetsAt"`
}

type nativeLimits struct {
	Primary   *nativeLimitWindow `json:"primary"`
	Secondary *nativeLimitWindow `json:"secondary"`
}

func (w *nativeLimitWindow) window() *LimitWindow {
	if w == nil {
		return nil
	}
	out := &LimitWindow{UsedPercent: w.UsedPercent}
	if w.Minutes != nil {
		out.Minutes = *w.Minutes
	}
	if w.ResetsAt != nil {
		at := time.Unix(*w.ResetsAt, 0).UTC()
		out.ResetsAt = &at
	}
	return out
}

// mergeLimits applies a snapshot, which may be sparse: a window it leaves
// out keeps its last value.
func (o *observer) mergeLimits(l nativeLimits) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if w := l.Primary.window(); w != nil {
		o.primary = w
	}
	if w := l.Secondary.window(); w != nil {
		o.secondary = w
	}
	if l.Primary != nil || l.Secondary != nil {
		o.limitsAt = time.Now().UTC()
	}
}

// usage is the last observed context and account limits; nil is unmeasured.
func (o *observer) usage() (*ContextUse, *AccountLimits) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var c *ContextUse
	if o.context != nil {
		copied := *o.context
		c = &copied
	}
	var l *AccountLimits
	for _, w := range []*LimitWindow{o.primary, o.secondary} {
		if w == nil {
			continue
		}
		if l == nil {
			l = &AccountLimits{ObservedAt: o.limitsAt}
		}
		l.Windows = append(l.Windows, *w)
	}
	return c, l
}

type threadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags"`
}

// handle runs on the native reader; dispatch has already dropped other
// threads' events, including a subagent's.
func (o *observer) handle(m nativeMessage) {
	switch m.Method {
	case "thread/tokenUsage/updated":
		var p struct {
			TokenUsage struct {
				Last struct {
					TotalTokens int64 `json:"totalTokens"`
				} `json:"last"`
				Window *int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		// Without the model's window there is no fill to report.
		if json.Unmarshal(m.Params, &p) == nil && p.TokenUsage.Window != nil && *p.TokenUsage.Window > 0 {
			o.mu.Lock()
			o.context = &ContextUse{Tokens: p.TokenUsage.Last.TotalTokens, Window: *p.TokenUsage.Window, ObservedAt: time.Now().UTC()}
			o.mu.Unlock()
		}
		return
	case "account/rateLimits/updated":
		var p struct {
			RateLimits nativeLimits `json:"rateLimits"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			o.mergeLimits(p.RateLimits)
		}
		return
	case "thread/status/changed":
	default:
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
	// The account's limits, read now and again every limitsPollEvery while
	// this connection lasts: Codex sends updates only as turns run, so an
	// idle fleet's limits would otherwise go stale. A Codex that cannot say
	// leaves them unmeasured.
	l.readLimits(ctx, c)
	go l.pollLimits(ctx, c)
	return nil
}

// limitsPollEvery is how often a connected runtime asks for the account's
// limits; the panel calls a reading older than two minutes stale.
var limitsPollEvery = time.Minute

func (l *nativeLink) readLimits(ctx context.Context, c *nativeControl) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var limits struct {
		RateLimits nativeLimits `json:"rateLimits"`
	}
	if c.call(ctx, "account/rateLimits/read", map[string]any{"excludeResetCreditDetails": true}, &limits) == nil {
		l.obs.mergeLimits(limits.RateLimits)
	}
}

func (l *nativeLink) pollLimits(ctx context.Context, c *nativeControl) {
	tick := time.NewTicker(limitsPollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-tick.C:
			l.readLimits(ctx, c)
		}
	}
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
