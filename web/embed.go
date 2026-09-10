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
// Plain patterns, deliberately without the "all:" prefix: "all:" exists only
// to make go:embed walk a directory INCLUDING files that start with "." or
// "_" — .DS_Store, editor swap files, scratch files — which is precisely what
// must never end up in the binary or be reachable over HTTP. Without "all:",
// directory walking (js/) already skips those, and on plain file patterns
// (index.html, app.css) the prefix would have been a no-op anyway.
//
// Only what exists today is listed. web/vendor/ (xterm.js) is added to this
// line by the task that vendors it; embedding a pattern that matches nothing
// is a compile-time error, so listing it early would break this package's
// build before that directory exists.
//
//go:embed index.html app.css js
var FS embed.FS
