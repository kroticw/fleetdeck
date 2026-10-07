package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kroticw/fleetdeck/internal/board"
)

// markdownExt is the only extension this section serves. The documentation
// section renders markdown (spec section 4), and a documentation root is an
// ordinary directory an operator pointed at — often one holding more than
// documentation. Serving anything that happens to sit there would turn a
// configured directory into a file server.
const markdownExt = ".md"

// errOutsideDocs is what confineToDocs returns for a path that does not resolve
// to somewhere inside one of the configured roots, whether it climbed out with
// .., named somewhere else outright, or went through a symlink pointing away.
var errOutsideDocs = errors.New("path is outside the configured documentation roots")

// Doc is one markdown file the panel may open.
//
// Path is absolute and already resolved, and is what the content route is called
// with. Title is the path relative to the root it was found under, which is what
// makes a list built from several roots readable: two roots may each hold an
// index.md, and only the relative path tells them apart. Root is carried so the
// panel can group by it rather than guessing from a common prefix.
type Doc struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Root  string `json:"root"`
	// Session is the short id of the session that wrote the document, from the
	// document's own frontmatter; empty, and left out, when it names none. The
	// panel opens that session next to the document.
	Session string `json:"session,omitempty"`
}

// handleDocsList walks every configured root and returns the markdown under them.
//
// Three outcomes are deliberately distinct, because the panel shows a different
// thing for each and collapsing any two of them lies to the operator:
//
//   - no roots configured at all — 404 saying so. An empty list here would read
//     as "there is no documentation", which is a different statement.
//   - roots configured and none of them readable — 404 naming them. A directory
//     that was renamed or never created is a configuration mistake, and hiding it
//     behind an empty list sends the operator looking for the wrong problem.
//   - roots readable — 200 with the documents, an empty array included. That
//     really does mean "these directories hold no markdown".
func (d Deps) handleDocsList(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if len(d.DocsRoots) == 0 {
		fail(w, http.StatusNotFound, "no documentation roots are configured")
		return
	}

	// Never nil: an empty documentation set has to reach the browser as [] and
	// not as null, which is not something the panel can map over.
	out := make([]Doc, 0)
	unreadable := walkDocs(d.DocsRoots, func(realRoot, resolved, rel string) {
		out = append(out, Doc{Path: resolved, Title: rel, Root: realRoot, Session: board.DocSession(resolved)})
	})

	if len(unreadable) == len(d.DocsRoots) {
		fail(w, http.StatusNotFound, "no configured documentation root could be read: "+strings.Join(unreadable, ", "))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// DocsRevision names the state of the documents under roots: it moves when a
// document the list would show appears, goes, or is rewritten, and stays put
// otherwise. It is empty when there are no roots.
//
// The panel lists the documents when a card opens, and an agent writes its
// report while the card is open (T-138); the snapshot carries this so the card
// asks for the list again when it moves. It is computed on every collect cycle
// rather than by watching the roots: a root is a whole tree, fsnotify does not
// watch recursively, and on macOS its kqueue backend holds a descriptor for
// every file in every watched directory. Walking the same files the list walks
// costs a stat each, and reads none of them.
func DocsRevision(roots []string) string {
	if len(roots) == 0 {
		return ""
	}
	h := sha256.New()
	walkDocs(roots, func(_, resolved, _ string) {
		info, err := os.Stat(resolved)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d\n", resolved, info.Size(), info.ModTime().UnixNano())
	})
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// walkDocs calls visit for every document the list shows, each once, and
// returns the roots that could not be read at all. Shared by the list and its
// revision, so that the two can never disagree on what a document is.
func walkDocs(roots []string, visit func(realRoot, resolved, rel string)) []string {
	seen := make(map[string]bool)
	var unreadable []string

	for _, root := range roots {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			unreadable = append(unreadable, root)
			continue
		}
		// WalkDir's own error is the one from reading the root itself; the
		// callback swallows everything below it, so an unreadable subdirectory
		// costs its own contents and not the whole listing.
		if err := filepath.WalkDir(realRoot, func(p string, e fs.DirEntry, walkErr error) error {
			if walkErr != nil || e.IsDir() || !isMarkdown(e.Name()) {
				return nil
			}
			// WalkDir does not follow directory symlinks but does hand over a
			// symlinked file, so this is where a link pointing out of the root is
			// dropped. Listing one would advertise a document that the content
			// route then refuses — a broken link the operator cannot explain.
			resolved, err := resolveExisting(p)
			if err != nil || !inside(realRoot, resolved) || seen[resolved] {
				return nil
			}
			rel, err := filepath.Rel(realRoot, resolved)
			if err != nil {
				return nil
			}
			seen[resolved] = true
			visit(realRoot, resolved, rel)
			return nil
		}); err != nil {
			unreadable = append(unreadable, root)
		}
	}
	return unreadable
}

// handleDocsContent returns one document, by the path the list handed out.
//
// The path arrives from the browser, so it is confined before anything opens it:
// without that, a crafted path reads any file the process can open (spec section
// 8, the same rule the card write follows).
func (d Deps) handleDocsContent(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	if len(d.DocsRoots) == 0 {
		// Nothing to confine a path to. Reading it anyway is the whole hole.
		fail(w, http.StatusNotFound, "no documentation roots are configured")
		return
	}
	p := r.URL.Query().Get("path")
	if p == "" {
		fail(w, http.StatusBadRequest, "path is required: a document request must name the document it wants")
		return
	}
	resolved, err := d.confineToDocs(p)
	if err != nil {
		// The refusal says what the rule is and nothing about what is there:
		// whether the path exists is itself an answer this route does not owe a
		// caller who is outside the roots.
		fail(w, http.StatusForbidden, "documents are served only from the configured documentation roots")
		return
	}
	if !isMarkdown(resolved) {
		fail(w, http.StatusForbidden, "only markdown documents are served")
		return
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": resolved, "body": string(raw)})
}

// confineToDocs turns the path a document request arrived with into an absolute
// path inside one of the configured roots, or refuses it.
//
// Both sides are resolved through their symlinks before they are compared, for
// the reason spec section 8 gives and cardpath.go states at length: a string
// prefix proves nothing in either direction. A symlink inside a root pointing out
// of it passes any prefix test while reading somewhere else, and on macOS /var is
// itself a symlink to /private/var, so a resolved document under an unresolved
// root looks foreign when it is not.
//
// A relative path is taken as relative to each root in turn, which is the only
// place a document can be. It is never resolved against the process's working
// directory, which has nothing to do with where the documentation is.
func (d Deps) confineToDocs(path string) (string, error) {
	for _, root := range d.DocsRoots {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			// This root cannot be resolved, so nothing can be shown to be inside
			// it. Another configured root may still hold the document.
			continue
		}
		candidate := path
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(realRoot, candidate)
		}
		resolved, err := resolveExisting(candidate)
		if err != nil {
			continue
		}
		if inside(realRoot, resolved) {
			return resolved, nil
		}
	}
	return "", errOutsideDocs
}

// inside reports whether the resolved path sits under the resolved root. The
// separator is part of the comparison so that a sibling directory whose name
// merely starts with the root's — /docs-private next to /docs — is not taken for
// a child of it.
func inside(realRoot, resolved string) bool {
	return strings.HasPrefix(resolved, realRoot+string(filepath.Separator))
}

func isMarkdown(name string) bool {
	return strings.EqualFold(filepath.Ext(name), markdownExt)
}
