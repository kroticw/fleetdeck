package server

import (
	"net/http"

	"github.com/kroticw/fleetdeck/web"
)

// contentSecurityPolicy is set on every static response. This PR is where
// fleetdeck starts serving HTML at all, so the policy belongs here rather than
// scattered across the later tasks that will need it most — in particular the
// hand-rolled markdown renderer that puts card bodies, written by autonomous
// agents, into the DOM of a page that can type into a live session. Treat that
// as an injection sink from day one, not from the day the renderer lands.
//
// style-src stays 'self': the vendored xterm.css is a file and works under it,
// so there is no reason to relax it to 'unsafe-inline'.
const contentSecurityPolicy = "default-src 'self'; connect-src 'self' ws: wss:; script-src 'self'; style-src 'self'"

// staticHandler serves the embedded frontend. A missing file is a 404: silently
// falling back to index.html would hide a typo in an import path until runtime.
//
// web.FS is already rooted at web/ — its go:embed patterns are relative to that
// package's own directory, so index.html, app.css and js/ sit at its root with
// no "web/" prefix to strip. That is why no fs.Sub is needed here, unlike an
// embed rooted one directory higher.
func staticHandler() http.Handler {
	fileServer := http.FileServerFS(web.FS)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		fileServer.ServeHTTP(w, r)
	})
}
