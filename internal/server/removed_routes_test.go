package server

import (
	"net/http"
	"testing"
)

// The session panel was the only client of these three routes: it read the
// digest, typed through its input box and uploaded pasted images. The panel is
// a live terminal now — text and a pasted image both go through it — and
// nothing else in this repository calls them, so they are not served. A route
// left without a client is a door into a live session that nobody watches.
func TestRoutesWithoutAClientAreNotServed(t *testing.T) {
	d, calls := testDeps()
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPost, "/api/sessions/abc123/text", `{"text":"hi"}`},
		{http.MethodGet, "/api/sessions/abc123/digest", ""},
		{http.MethodPost, "/api/sessions/abc123/image", `{"data":"iVBORw0KGgo="}`},
	} {
		rec := do(d, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: status %d, want the route gone (404 or 405): %s", tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}
	if len(*calls) != 0 {
		t.Errorf("a removed route still reached its dependency: %v", *calls)
	}
}
