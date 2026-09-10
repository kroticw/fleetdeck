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
func (f *Fetcher) Limits(ctx context.Context) (Limits, error) {
	f.mu.Lock()
	if !f.at.IsZero() && time.Since(f.at) < f.ttl {
		defer f.mu.Unlock()
		return f.cached, nil
	}
	f.mu.Unlock()

	tok, err := f.token()
	if err != nil {
		return Limits{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoint, nil)
	if err != nil {
		return Limits{}, fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Limits{}, fmt.Errorf("usage request: %w", err)
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
		return Limits{}, fmt.Errorf("decode usage reply: %w", err)
	}
	if payload.Error != nil {
		return Limits{}, fmt.Errorf("usage endpoint refused: %s", payload.Error.Type)
	}
	if payload.FiveHour == nil || payload.SevenDay == nil {
		return Limits{}, fmt.Errorf("usage reply carries no windows")
	}

	fiveHourResets, err := time.Parse(time.RFC3339, payload.FiveHour.ResetsAt)
	if err != nil {
		return Limits{}, fmt.Errorf("parse five_hour resets_at: %w", err)
	}
	sevenDayResets, err := time.Parse(time.RFC3339, payload.SevenDay.ResetsAt)
	if err != nil {
		return Limits{}, fmt.Errorf("parse seven_day resets_at: %w", err)
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
