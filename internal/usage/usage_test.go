package usage

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const body = `{"five_hour":{"utilization":17.4,"resets_at":"2026-09-09T12:00:00.000Z"},"seven_day":{"utilization":48.2,"resets_at":"2026-09-13T00:00:00.000Z"}}`

func TestLimitsParsesBothWindows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
			t.Errorf("beta header missing, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("token not sent, got %q", got)
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	got, err := f.Limits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.FiveHour.Utilization != 17.4 || got.SevenDay.Utilization != 48.2 {
		t.Fatalf("utilization wrong: %+v", got)
	}
	if got.FiveHour.ResetsAt.IsZero() || got.SevenDay.ResetsAt.IsZero() {
		t.Fatal("reset timestamps must be parsed")
	}
}

func TestLimitsCachesWithinTTL(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := f.Limits(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Fatalf("three calls within the TTL must hit the endpoint once, got %d", hits)
	}
}

func TestExpiredTokenIsAnErrorNotZeroes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	if _, err := f.Limits(context.Background()); err == nil {
		t.Fatal("an auth error must be reported; zero gauges would look like a healthy account")
	}
}

func TestMissingTokenIsTypedError(t *testing.T) {
	f := NewFetcher(func() (string, error) { return "", ErrNoToken }, "http://127.0.0.1:1", time.Minute)
	if _, err := f.Limits(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestEmptyBodyIsNotSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	if _, err := f.Limits(context.Background()); err == nil {
		t.Fatal("a response without windows must fail; nothing to read is not a healthy zero")
	}
}
