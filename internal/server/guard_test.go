package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func jsonRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

// The probe below is the attack as it was actually run against this handler
// before there was anything here to stop it: a page on another site posting into
// a live session with a CORS-simple content type, which a browser sends without
// asking the server first. It answered 204 and typed "whoami" into the session.
// The attacker cannot read the response and does not need to.
func TestTheCrossOriginProbeThatReachedASessionIsRefused(t *testing.T) {
	d, calls := testDeps()
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/abc123/text", strings.NewReader(`{"text":"whoami"}`))
	r.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	r.Header.Set("Origin", "https://evil.example")

	rec := send(d, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the cross-origin probe must be refused with 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the daemon, got %v", *calls)
	}
}

// Every route, not just the writes: a foreign page reading the snapshot has the
// whole fleet's state — prompts, card contents, session names.
func TestAForeignOriginIsRefusedOnEveryRoute(t *testing.T) {
	cases := []struct {
		name    string
		request func() *http.Request
	}{
		{"snapshot", func() *http.Request { return httptest.NewRequest(http.MethodGet, "/api/snapshot", nil) }},
		{"text", func() *http.Request { return jsonRequest(http.MethodPost, "/api/sessions/a/text", `{"text":"hi"}`) }},
		{"cards", func() *http.Request {
			return jsonRequest(http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"done"}`)
		}},
		{"status", func() *http.Request {
			return jsonRequest(http.MethodPost, "/api/status", `{"sessionId":"a","model":"m","costUSD":0,"contextPercent":0}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, calls := testDeps()
			r := tc.request()
			r.Header.Set("Origin", "https://evil.example")
			rec := send(d, r)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("a foreign origin must be refused with 403, got %d: %s", rec.Code, rec.Body.String())
			}
			if len(*calls) != 0 {
				t.Fatalf("nothing must reach the fleet, got %v", *calls)
			}
		})
	}
}

func TestOriginsThatAreNotThePanelsOwnAreRefused(t *testing.T) {
	for _, origin := range []string{
		"https://evil.example",
		"http://evil.example:7777",
		// Neither of these is loopback: the check is on the host, not on the
		// text of the origin, so a hostname that merely contains one is not it.
		"http://localhost.evil.example",
		"http://127.0.0.1.evil.example",
		// What a sandboxed iframe or a file:// page sends.
		"null",
		// A browser extension's own origin is not a page this panel served.
		"moz-extension://a1b2c3",
		"chrome-extension://a1b2c3",
	} {
		t.Run(origin, func(t *testing.T) {
			d, calls := testDeps()
			r := jsonRequest(http.MethodPost, "/api/sessions/abc/text", `{"text":"hi"}`)
			r.Header.Set("Origin", origin)
			if rec := send(d, r); rec.Code != http.StatusForbidden {
				t.Fatalf("origin %q must be refused with 403, got %d: %s", origin, rec.Code, rec.Body.String())
			}
			if len(*calls) != 0 {
				t.Fatalf("nothing must reach the daemon, got %v", *calls)
			}
		})
	}
}

func TestThePanelsOwnOriginIsAccepted(t *testing.T) {
	for _, origin := range []string{
		"http://127.0.0.1:7777",
		"http://localhost:7777",
		"http://[::1]:7777",
	} {
		t.Run(origin, func(t *testing.T) {
			d, calls := testDeps()
			r := jsonRequest(http.MethodPost, "/api/sessions/abc/text", `{"text":"hi"}`)
			r.Header.Set("Origin", origin)
			if rec := send(d, r); rec.Code != http.StatusNoContent {
				t.Fatalf("the panel's own origin must be served, got %d: %s", rec.Code, rec.Body.String())
			}
			if len(*calls) != 1 {
				t.Fatalf("the request must reach the daemon, got %v", *calls)
			}
		})
	}
}

// cmd/fleetdeck-status posts exactly this shape: application/json and no Origin
// at all, because it is not a browser. Refusing an absent Origin would take the
// only source of a session's model, cost and context percentage off the panel.
func TestTheStatuslineReportersRequestStillSucceeds(t *testing.T) {
	d, calls := testDeps()
	r := jsonRequest(http.MethodPost, "/api/status", `{"sessionId":"abc-123","model":"Opus","costUSD":1.25,"contextPercent":42.5}`)
	if r.Header.Get("Origin") != "" {
		t.Fatal("this test is meaningless if the reporter sends an Origin")
	}
	if rec := send(d, r); rec.Code != http.StatusNoContent {
		t.Fatalf("the reporter must keep working, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("the report must reach the store, got %v", *calls)
	}
}

// The content type check is the second layer, and it stands on its own: even
// from an origin the panel accepts, a body that is not JSON is refused. That is
// what removes the simple-request path — a cross-origin application/json POST
// needs a preflight this server never approves.
func TestANonJSONBodyIsRefusedWithUnsupportedMediaType(t *testing.T) {
	for _, contentType := range []string{
		"text/plain;charset=UTF-8",
		"application/x-www-form-urlencoded",
		"multipart/form-data; boundary=x",
		"application/json-patch+json",
		"",
	} {
		t.Run(contentType, func(t *testing.T) {
			d, calls := testDeps()
			r := httptest.NewRequest(http.MethodPost, "/api/sessions/abc123/text", strings.NewReader(`{"text":"whoami"}`))
			if contentType != "" {
				r.Header.Set("Content-Type", contentType)
			}
			rec := send(d, r)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("content type %q must be refused with 415, got %d: %s", contentType, rec.Code, rec.Body.String())
			}
			if len(*calls) != 0 {
				t.Fatalf("nothing must reach the daemon, got %v", *calls)
			}
		})
	}
}

func TestAJSONContentTypeWithParametersIsAccepted(t *testing.T) {
	d, calls := testDeps()
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/abc/text", strings.NewReader(`{"text":"hi"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if rec := send(d, r); rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("the request must reach the daemon, got %v", *calls)
	}
}

// A GET carries no body, so it has no content type to check. Requiring one would
// break the panel's own reads for nothing.
func TestAReadWithoutABodyNeedsNoContentType(t *testing.T) {
	d, _ := testDeps()
	if rec := do(d, http.MethodGet, "/api/snapshot", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The preflight is the browser asking whether a cross-origin JSON request may be
// sent at all. This server answers no by saying nothing: without an
// Access-Control-Allow-Origin the browser never sends the real request. There is
// no legitimate cross-origin caller, so there is no CORS header to add.
func TestAPreflightIsNotGrantedCORSHeaders(t *testing.T) {
	d, _ := testDeps()
	r := httptest.NewRequest(http.MethodOptions, "/api/sessions/abc123/text", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "content-type")

	rec := send(d, r)
	if rec.Code == http.StatusOK || rec.Code == http.StatusNoContent {
		t.Fatalf("a preflight from a foreign origin must not be approved, got %d", rec.Code)
	}
	for _, header := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Credentials",
	} {
		if got := rec.Header().Get(header); got != "" {
			t.Fatalf("%s must never be sent, got %q", header, got)
		}
	}
}

// A same-origin preflight is not granted either, and does not need to be: the
// panel's own page is same-origin and its requests are never preflighted.
func TestNoCORSHeadersAreSentToTheLocalOriginEither(t *testing.T) {
	d, _ := testDeps()
	r := jsonRequest(http.MethodPost, "/api/sessions/abc/text", `{"text":"hi"}`)
	r.Header.Set("Origin", "http://127.0.0.1:7777")
	rec := send(d, r)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin must never be sent, got %q", got)
	}
}
