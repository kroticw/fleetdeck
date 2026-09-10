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

// FS holds index.html, app.css and every file under js/, rooted at this
// directory — so internal/server can serve FS directly with no further
// stripping of a leading "web/" prefix.
//
// Only what exists today is listed. web/vendor/ (xterm.js) is added to this
// line by the task that vendors it; embedding a pattern that matches nothing
// is a compile-time error, so listing it early would break this package's
// build before that directory exists.
//
//go:embed all:index.html all:app.css all:js
var FS embed.FS
