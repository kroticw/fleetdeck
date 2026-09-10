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
// style-src carries 'unsafe-inline' and nothing else does. It is there for one
// named reason: xterm.js 6.0.0, vendored in web/vendor/ and the only way the
// session panel draws a terminal, builds three <style> elements at runtime and
// fills them with textContent — the theme's colours, the measured cell height
// and the scrollbar. A <style> element's text is an inline style under CSP no
// matter who created it, so style-src 'self' blocks all three, and this version
// of xterm has no nonce option to use instead (the string "nonce" does not
// appear in the bundle). Serving xterm.css as a file does not help: the blocked
// rules carry values computed at runtime — `${this.dimensions.css.cell.height}px`
// for the cell metrics, the theme's foreground.css, and the fontFamily and
// fontSize read from the options service — and a static stylesheet cannot carry
// a measurement. The injection happens in addition to the file, not instead of
// it.
//
// This was not deduced. Under style-src 'self' the browser console fills with
// "Applying inline style violates the following Content Security Policy
// directive 'style-src 'self”… The action has been blocked", sourced to
// /vendor/xterm.js, and the terminal draws in a proportional font, with no
// colour, at the wrong line height, clipped at the right edge. With this
// keyword added and nothing else changed, the violations stop and the terminal
// draws correctly.
//
// Two things follow, for whoever reads this next. Removing 'unsafe-inline'
// breaks the terminal — check it against a running panel before deciding the
// keyword is unused. And it is not a precedent for widening anything else:
// script-src and default-src stay 'self', which is what keeps this an
// injection of appearance rather than of behaviour. A style can corrupt how
// this page looks; it cannot fetch, because img-src is unset and inherits
// default-src 'self', and it cannot run, because script-src does not carry the
// keyword.
const contentSecurityPolicy = "default-src 'self'; connect-src 'self' ws: wss:; script-src 'self'; style-src 'self' 'unsafe-inline'"

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
