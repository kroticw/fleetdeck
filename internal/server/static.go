package server

import (
	"html"
	"io/fs"
	"net/http"
	"strings"

	"github.com/kroticw/fleetdeck/internal/buildinfo"
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
//
// The document itself is the one exception to serving files as embedded: it
// carries the build fingerprint, see indexWithFingerprint. It is also sent
// with Cache-Control: no-cache. The embedded files have no modification time,
// so the file server sends no validator either, and what a WebView does with
// such a response on reload is not something to leave to it -- a document
// taken from cache after the banner's "reload" would bring the old
// fingerprint back, and the banner with it.
func staticHandler(build *buildinfo.Fingerprint) http.Handler {
	fileServer := http.FileServerFS(web.FS)
	index := indexWithFingerprint(build)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		if r.URL.Path != "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		if index == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

// headTag is where the fingerprint is inserted. The page reads it back with a
// selector, so its position inside <head> is all that matters; the real
// index.html is checked to have exactly one of these in the static tests.
const headTag = "<head>"

// indexWithFingerprint returns index.html with the running build's web hash in
// a <meta> tag, or nil to serve the page as embedded.
//
// The fingerprint rides in the document rather than being fetched by the page
// afterwards: the page must know which build it came from as of the moment it
// arrived. Asked any later -- the first snapshot on the socket, say -- the
// answer may already come from a panel that replaced this one in between,
// and the page would adopt a fingerprint that is not its own. A <meta> tag
// needs no script, so the Content-Security-Policy's script-src 'self' is not
// in the way.
func indexWithFingerprint(build *buildinfo.Fingerprint) []byte {
	if build == nil || build.Web == "" {
		return nil
	}
	data, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		return nil
	}
	page := string(data)
	if !strings.Contains(page, headTag) {
		return nil
	}
	meta := headTag + "\n  <meta name=\"fleetdeck-build\" content=\"" + html.EscapeString(build.Web) + "\">"
	return []byte(strings.Replace(page, headTag, meta, 1))
}
