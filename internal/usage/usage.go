package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Endpoint is the production usage endpoint. It is unofficial and gated behind a
// beta header: when it changes, the gauges go dark and the panel keeps working.
const Endpoint = "https://api.anthropic.com/api/oauth/usage"

type Window struct {
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resetsAt"`
}

type Limits struct {
	FiveHour  Window    `json:"fiveHour"`
	SevenDay  Window    `json:"sevenDay"`
	FetchedAt time.Time `json:"fetchedAt"`
}

type Fetcher struct {
	token    func() (string, error)
	endpoint string
	ttl      time.Duration

	mu     sync.Mutex
	cached Limits
	at     time.Time
}

func NewFetcher(token func() (string, error), endpoint string, ttl time.Duration) *Fetcher {
	return &Fetcher{token: token, endpoint: endpoint, ttl: ttl}
}

// Limits returns the account windows, cached for the fetcher's TTL so one
// request serves every session instead of one request per session.
//
// A failed refresh (network blip, a token call that briefly errors, a
// malformed reply) returns that error alongside the last successfully
// fetched value, not a zero Limits -- see staleCache. A caller that only
// checks the error still gets the old "gauges go dark" behavior; one that
// reads the returned Limits too can show a calm "last known, N minutes ago"
// instead of blanking on every transient failure between two good fetches
// a TTL apart. There is nothing to fall back to before the first successful
// fetch ever completes; staleCache reports that honestly as a zero Limits.
func (f *Fetcher) Limits(ctx context.Context) (Limits, error) {
	f.mu.Lock()
	if !f.at.IsZero() && time.Since(f.at) < f.ttl {
		defer f.mu.Unlock()
		return f.cached, nil
	}
	f.mu.Unlock()

	tok, err := f.token()
	if err != nil {
		return f.staleCache(), err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoint, nil)
	if err != nil {
		return f.staleCache(), fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return f.staleCache(), fmt.Errorf("usage request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var payload struct {
		Error *struct {
			Type string `json:"type"`
		} `json:"error"`
		FiveHour *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return f.staleCache(), fmt.Errorf("decode usage reply: %w", err)
	}
	if payload.Error != nil {
		return f.staleCache(), fmt.Errorf("usage endpoint refused: %s", payload.Error.Type)
	}
	if payload.FiveHour == nil || payload.SevenDay == nil {
		return f.staleCache(), fmt.Errorf("usage reply carries no windows")
	}

	fiveHourResets, err := time.Parse(time.RFC3339, payload.FiveHour.ResetsAt)
	if err != nil {
		return f.staleCache(), fmt.Errorf("parse five_hour resets_at: %w", err)
	}
	sevenDayResets, err := time.Parse(time.RFC3339, payload.SevenDay.ResetsAt)
	if err != nil {
		return f.staleCache(), fmt.Errorf("parse seven_day resets_at: %w", err)
	}
	out := Limits{
		FiveHour:  Window{Utilization: payload.FiveHour.Utilization, ResetsAt: fiveHourResets},
		SevenDay:  Window{Utilization: payload.SevenDay.Utilization, ResetsAt: sevenDayResets},
		FetchedAt: time.Now(),
	}
	f.mu.Lock()
	f.cached, f.at = out, time.Now()
	f.mu.Unlock()
	return out, nil
}

// staleCache returns the last successfully fetched Limits, regardless of how
// long ago that was -- f.at is not reset on a failed refresh (a fresh
// attempt is retried on the very next call, per the TTL check above), so a
// stale value here always means "the last real fetch was this", never a
// value invalidated by time alone. Callers pair it with the error the
// refresh actually failed with and decide how old is too old to show; this
// method only ever answers "what did we last know", not "is that recent
// enough". A zero Limits (its own FetchedAt is the zero time.Time) means no
// fetch has ever succeeded -- there is nothing to fall back to yet.
func (f *Fetcher) staleCache() Limits {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cached
}
