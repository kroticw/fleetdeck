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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	_, err := f.Limits(context.Background())
	if err == nil {
		t.Fatal("an auth error must be reported; zero gauges would look like a healthy account")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("an authentication_error body must be ErrUnauthorized, got %v", err)
	}
}

// TestAnHTTP401IsErrUnauthorizedEvenWithAnUnnamedErrorType covers the other
// half of the ErrUnauthorized check: the status code alone, independent of
// whatever the body's error.type happens to say -- the panel's "sign-in
// needed" wording must not depend on the endpoint naming its own error the
// way this test's body deliberately does not.
func TestAnHTTP401IsErrUnauthorizedEvenWithAnUnnamedErrorType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"type":"some_future_type_nobody_wrote_a_case_for"}}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	if _, err := f.Limits(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("HTTP 401 must be ErrUnauthorized regardless of error.type, got %v", err)
	}
}

// TestRateLimitErrorIsDistinguishedFromAuthFailure is the fix this task
// exists for: a rate_limit_error reply used to collapse into the exact same
// generic error an expired token produces, and the panel's text could not
// tell them apart -- "sign-in needed" is wrong advice for an account that
// is simply being asked to slow down.
func TestRateLimitErrorIsDistinguishedFromAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"rate_limit_error","message":"Rate limited. Please try again later."}}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	_, err := f.Limits(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Fatal("a rate limit must never also read as ErrUnauthorized")
	}
}

// TestAnUnclassifiedErrorTypeIsNeitherSentinel covers the third bucket: an
// error this package has no specific case for must not silently fall into
// either sentinel, which would make the panel promise something ("sign-in
// needed" or "it will recover on its own") it has no basis for.
func TestAnUnclassifiedErrorTypeIsNeitherSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"error":{"type":"overloaded_error"}}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	_, err := f.Limits(context.Background())
	if err == nil {
		t.Fatal("an error body must still be reported as an error")
	}
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrRateLimited) {
		t.Fatalf("an unrecognized error.type must not match either sentinel, got %v", err)
	}
}

func TestMissingTokenIsTypedError(t *testing.T) {
	f := NewFetcher(func() (string, error) { return "", ErrNoToken }, "http://127.0.0.1:1", time.Minute)
	if _, err := f.Limits(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("want ErrNoToken, got %v", err)
	}
}

func TestEmptyBodyIsNotSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	if _, err := f.Limits(context.Background()); err == nil {
		t.Fatal("a response without windows must fail; nothing to read is not a healthy zero")
	}
}

func TestMalformedResetsAtIsAnErrorNotZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"five_hour":{"utilization":17.4,"resets_at":"invalid"},"seven_day":{"utilization":48.2,"resets_at":"2026-09-13T00:00:00.000Z"}}`))
	}))
	defer srv.Close()

	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, time.Minute)
	if _, err := f.Limits(context.Background()); err == nil {
		t.Fatal("a response with malformed resets_at must fail; zero timestamps would look like no reset pending")
	}
}

// TestARefreshFailureReturnsTheLastGoodValueAlongsideTheError is the fix for the
// reported flicker: a single transient failure between two successful fetches a TTL
// apart used to return a zero Limits, which the panel rendered as "—" for one poll
// cycle before the next successful fetch brought the numbers back — a blink driven
// by the source discarding its own cache, not by anything in the browser. The cache
// must survive a failed refresh so a caller can keep showing the last known value
// (aged, not asserted fresh) instead of blanking on every blip.
func TestARefreshFailureReturnsTheLastGoodValueAlongsideTheError(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	// A short TTL so the second call below is forced to attempt a real refresh
	// rather than serving the first call's cache hit unconditionally.
	f := NewFetcher(func() (string, error) { return "tok", nil }, srv.URL, 10*time.Millisecond)

	good, err := f.Limits(context.Background())
	if err != nil {
		t.Fatalf("first fetch must succeed to seed the cache: %v", err)
	}

	time.Sleep(20 * time.Millisecond) // past the TTL
	fail = true
	stale, err := f.Limits(context.Background())
	if err == nil {
		t.Fatal("a failed refresh must still report its error")
	}
	if stale != good {
		t.Fatalf("a failed refresh must return the last good value, got %+v want %+v", stale, good)
	}
}

// TestNoCacheYetIsAZeroLimitsNotAPanic covers the floor of staleCache: a failure on
// the very first fetch has nothing to fall back to, and must say so honestly (a zero
// Limits) rather than fabricate a value or fail to compile a nil case.
func TestNoCacheYetIsAZeroLimitsNotAPanic(t *testing.T) {
	f := NewFetcher(func() (string, error) { return "", ErrNoToken }, "http://127.0.0.1:1", time.Minute)
	got, err := f.Limits(context.Background())
	if err == nil {
		t.Fatal("a missing token must still be reported as an error")
	}
	if got != (Limits{}) {
		t.Fatalf("no fetch has ever succeeded; want a zero Limits, got %+v", got)
	}
}
