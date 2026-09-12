// Package web embeds the fleetdeck frontend — the static HTML, CSS and ES
// modules the panel serves to the browser.
//
// The embed directive lives here, next to the files it embeds, rather than in
// internal/server where the rest of the HTTP surface lives. A go:embed pattern
// is resolved relative to the file that contains it and cannot climb out of its
// own package directory (no "..", no absolute path), so a directive in
// internal/server/static.go has no way to reach a web/ directory that sits at
// the repository root one level up from internal/. Anchoring the directive in
// package web instead keeps every pattern legal: relative to this directory,
// index.html, app.css and js/ are all children, not ancestors.
package web

import "embed"

// FS holds index.html, app.css and every file under js/ and vendor/, rooted at
// this directory — so internal/server can serve FS directly with no further
// stripping of a leading "web/" prefix.
//
// Plain patterns, deliberately without the "all:" prefix: "all:" exists only
// to make go:embed walk a directory INCLUDING files that start with "." or
// "_" — .DS_Store, editor swap files, scratch files — which is precisely what
// must never end up in the binary or be reachable over HTTP. Without "all:",
// directory walking (js/) already skips those, and on plain file patterns
// (index.html, app.css) the prefix would have been a no-op anyway.
//
// Only what exists today is listed. vendor/ joined the list when xterm.js was
// vendored into it; embedding a pattern that matches nothing is a compile-time
// error, so a directory is listed here only once it holds files. That also
// makes this line a check of its own: delete web/vendor/ and this package stops
// compiling, rather than the panel quietly serving a 404 for a <script> tag no
// browser reports back to us.
//
// setup.html is the page a panel with no configuration serves in place of
// index.html (internal/server/setup.go).
//
// start.html is the start page, served at "/" when the address names no fleet
// (internal/server/static.go). A top-level file has to be named here: the js
// and vendor patterns walk their directories, but a new file beside index.html
// is matched by nothing and would simply not be in the binary — with no
// compile error to say so, since the other patterns still match.
//
//go:embed index.html setup.html start.html app.css js vendor
var FS embed.FS
