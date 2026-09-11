package server

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/kroticw/fleetdeck/web"
)

// SetupStep is one step of setting a panel up, as the setup page shows it: what
// it did, or — Error set — what it refused to do and why.
type SetupStep struct {
	Name   string `json:"name"`
	Note   string `json:"note,omitempty"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// SetupDeps is what a panel with no configuration file serves from: the
// workspace it proposes, and the one write it performs.
type SetupDeps struct {
	// DefaultWorkspace is the directory the setup page proposes.
	DefaultWorkspace string
	// Setup makes the workspace at path and the configuration naming it, and
	// reports every step. ok says whether the panel can now run on what was
	// made; an error means path itself was refused and nothing was done.
	Setup func(path string) (steps []SetupStep, ok bool, err error)
}

// NewSetup is the HTTP surface of a panel that has no configuration yet: the
// setup page at "/", the files that page needs, and the route that performs
// the setup. The panel's own routes answer 503 until it is set up, saying so.
//
// The same guard as the panel's: setup writes into the person's home
// directory, and a page on another site must not be able to trigger that.
func NewSetup(sd SetupDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"default": sd.DefaultWorkspace})
	})
	mux.HandleFunc("POST /api/setup", sd.handleSetup)
	mux.HandleFunc("/api/", notSetUp)
	mux.HandleFunc("/ws", notSetUp)
	// "/" rather than "GET /": the latter conflicts with "/api/" in ServeMux's
	// precedence rules. setupStatic refuses what is not a read itself.
	mux.Handle("/", setupStatic())
	return guard(mux)
}

func (sd SetupDeps) handleSetup(w http.ResponseWriter, r *http.Request) {
	if sd.Setup == nil {
		unavailable(w, "a setup")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	steps, ok, err := sd.Setup(body.Path)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "steps": steps})
}

func notSetUp(w http.ResponseWriter, _ *http.Request) {
	fail(w, http.StatusServiceUnavailable, "this panel is not set up yet: open its page to choose the folder for its board and docs")
}

// setupStatic serves setup.html at "/" and the embedded files beside it.
//
// No ETag, unlike the panel's own page (static.go): both live at "/", and a
// validator stored for this page would be sent again with the reload that
// follows setup — and the panel, which tags its page with the same build
// hash, would answer 304 and leave this page on screen.
func setupStatic() http.Handler {
	fileServer := http.FileServerFS(web.FS)
	page, err := fs.ReadFile(web.FS, "setup.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			fail(w, http.StatusMethodNotAllowed, "the setup page is read, not written to")
			return
		}
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/" {
			if strings.HasSuffix(r.URL.Path, "/index.html") {
				// The panel's page would open a socket to a panel that is not there.
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "the setup page is missing from this build")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
}
