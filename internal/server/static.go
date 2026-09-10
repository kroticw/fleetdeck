package server

import (
	"net/http"

	"github.com/kroticw/fleetdeck/web"
)

// staticHandler serves the embedded frontend. A missing file is a 404: silently
// falling back to index.html would hide a typo in an import path until runtime.
//
// web.FS is already rooted at web/ — its go:embed patterns are relative to that
// package's own directory, so index.html, app.css and js/ sit at its root with
// no "web/" prefix to strip. That is why no fs.Sub is needed here, unlike an
// embed rooted one directory higher.
func staticHandler() http.Handler {
	return http.FileServerFS(web.FS)
}
