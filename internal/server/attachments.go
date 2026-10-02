package server

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kroticw/fleetdeck/internal/board"
)

// handleAttachment serves one file under the board's attachments directory,
// named by its place there: "T-068/shot.png". A picture is served as one, so a
// card can show it; anything else as a download under a policy that runs
// nothing, because this origin can type into a live session and a dropped file
// is not the panel's own page.
func (d Deps) handleAttachment(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if d.BoardDir == "" {
		unavailable(w, "a board directory")
		return
	}
	rel := r.URL.Query().Get("path")
	if !filepath.IsLocal(rel) {
		fail(w, http.StatusBadRequest, "path must name a file under the board's attachments")
		return
	}
	root, err := confineToBoard(d.BoardDir, "attachments")
	if err != nil {
		fail(w, http.StatusNotFound, "this board has no attachments")
		return
	}
	full, err := confineToBoard(d.BoardDir, filepath.Join("attachments", rel))
	if err != nil || !strings.HasPrefix(full, root+string(filepath.Separator)) {
		fail(w, http.StatusForbidden, "attachments are served from the board's attachments directory only")
		return
	}
	f, err := os.Open(full)
	if errors.Is(err, fs.ErrNotExist) {
		fail(w, http.StatusNotFound, "no such attachment")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		fail(w, http.StatusNotFound, "no such attachment")
		return
	}

	name := filepath.Base(full)
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if board.IsImage(name) {
		h.Set("Content-Type", mime.TypeByExtension(strings.ToLower(path.Ext(name))))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}
