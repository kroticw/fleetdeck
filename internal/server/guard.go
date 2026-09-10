package server

import (
	"mime"
	"net"
	"net/http"
	"net/url"
)

// guard is the check every request passes before any handler sees it.
//
// The panel listens on 127.0.0.1 (spec section 8), which keeps other machines
// out and does nothing at all about the browser running on this one: a page on
// any site the operator has open can fetch() a loopback URL, and the routes here
// type text into a live Claude Code session. Two independent layers close that,
// each covering what the other does not.
//
//   - Origin. A browser attaches it to every cross-origin request, so an origin
//     that is present and not the panel's own is a page that has no business
//     here (403). Absent is allowed: curl and cmd/fleetdeck-status send none, and
//     the statusline reporter must keep working.
//   - Content type. Every body must be application/json (415 otherwise). This is
//     what removes the simple-request path entirely: text/plain, form encoding
//     and multipart are the three content types a browser will send cross-origin
//     without asking first, and an application/json POST needs a preflight that
//     this server answers with no CORS headers at all, so the real request is
//     never sent.
//
// No CORS headers are ever written, deliberately. There is no legitimate
// cross-origin caller.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			refuseForeignOrigin(w)
			return
		}
		if carriesBody(r) && !isJSONRequest(r) {
			fail(w, http.StatusUnsupportedMediaType, "request bodies must be application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed reports whether a request's Origin header, if it has one, names
// a page this panel served itself. It is the single rule the HTTP routes and the
// WebSocket both apply — one function so the two cannot drift apart.
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || isLoopbackOrigin(origin)
}

func refuseForeignOrigin(w http.ResponseWriter) {
	fail(w, http.StatusForbidden, "this panel answers only pages it served itself")
}

// isLoopbackOrigin reports whether an origin is one of the loopback addresses the
// panel is reachable at. The scheme is checked as well as the host, so an
// extension origin or an opaque "null" — what a sandboxed iframe or a file:// page
// sends — is refused rather than parsed into something that looks local.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// carriesBody reports whether a request has a body to have a content type for.
// The three methods this API writes with always carry one; anything else counts
// as carrying a body only when it actually has one, which leaves a plain GET —
// and the WebSocket handshake — alone.
func carriesBody(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		// Length -1 is a chunked body of unknown size, which is still a body.
		return r.ContentLength != 0
	}
}

func isJSONRequest(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}
