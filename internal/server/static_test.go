package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/web"
)

func TestIndexIsServedAtRoot(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 at root, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "fleetdeck") {
		t.Fatal("index.html must be served at root")
	}
}

func TestStaticAssetIsServed(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for app.css, got %d", rec.Code)
	}
}

func TestMissingAssetIs404NotIndex(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/nope.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a missing asset must be 404, not a silent index page: got %d", rec.Code)
	}
}

func TestAPIRoutesStillWinOverStatic(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/snapshot", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "cards") {
		t.Fatalf("static handler must not shadow the API: %d %s", rec.Code, rec.Body.String())
	}
}

// TestEmbeddedFSIsNotEmpty guards against a go:embed directive that silently
// stops matching anything — a pattern typo, a rename that removed the last
// file a pattern reached. An embed.FS with zero files behind it serves 404 for
// everything, which looks identical to a correctly embedded but genuinely
// empty site; this test tells the two apart at build-test time instead of
// leaving it for someone to notice in a browser.
func TestEmbeddedFSIsNotEmpty(t *testing.T) {
	count := 0
	if err := fs.WalkDir(web.FS, ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	}); err != nil {
		t.Fatalf("walk embedded FS: %v", err)
	}
	if count == 0 {
		t.Fatal("embedded web FS holds zero files: this is a build-time mistake in the go:embed patterns, not an empty website")
	}
}

// TestEmbeddedFSContainsExpectedFiles asserts, by name, that the specific
// files this task creates are actually reachable through web.FS. Dropping one
// of them from the go:embed line still leaves the FS non-empty (the test
// above would pass), so that check alone would not have caught it.
func TestEmbeddedFSContainsExpectedFiles(t *testing.T) {
	for _, name := range []string{"index.html", "app.css", "js/store.js", "js/main.js"} {
		if _, err := fs.Stat(web.FS, name); err != nil {
			t.Errorf("expected file %q missing from embedded FS: %v", name, err)
		}
	}
}

// rootRefPattern finds every src="..." or href="..." attribute in an HTML
// document.
var rootRefPattern = regexp.MustCompile(`(?:src|href)="([^"]+)"`)

// jsImportPattern finds the specifier of every static import in a JS module,
// with or without a binding list in front of it (`import x from "..."` and
// `import "..."` both match).
var jsImportPattern = regexp.MustCompile(`import\s+(?:.*?\s+from\s+)?["'](\.[^"']+)["']`)

// TestStaticReferencesResolveWithinEmbeddedFS is the guard against the failure
// mode a frontend with no build step is defenceless against: nothing here
// compiles, so a renamed file leaves the Go build green and the page silently
// broken in the browser. It parses index.html for every root-relative
// src/href, and every JS module under js/ for every relative import
// specifier, and asserts each one resolves to a file that actually exists in
// the embedded FS.
func TestStaticReferencesResolveWithinEmbeddedFS(t *testing.T) {
	index, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	for _, m := range rootRefPattern.FindAllStringSubmatch(string(index), -1) {
		ref := m[1]
		if !strings.HasPrefix(ref, "/") {
			continue // not root-relative: e.g. a scheme'd URL, out of scope here
		}
		target := strings.TrimPrefix(ref, "/")
		if _, err := fs.Stat(web.FS, target); err != nil {
			t.Errorf("index.html references %q, which does not exist in the embedded FS", ref)
		}
	}

	if err := fs.WalkDir(web.FS, "js", func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(file, ".js") {
			return err
		}
		src, err := fs.ReadFile(web.FS, file)
		if err != nil {
			return err
		}
		for _, m := range jsImportPattern.FindAllStringSubmatch(string(src), -1) {
			spec := m[1]
			target := path.Join(path.Dir(file), spec)
			if _, err := fs.Stat(web.FS, target); err != nil {
				t.Errorf("%s imports %q, which resolves to %q and does not exist in the embedded FS", file, spec, target)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk js/: %v", err)
	}
}
