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

// The panel is served at the root with a fleet named. The bare root is the
// start page instead (internal/server/startpage_test.go).
func TestIndexIsServedAtRootForANamedFleet(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?fleet=", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 at root, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "fleetdeck") {
		t.Fatal("index.html must be served at root")
	}
}

func TestIndexHasContentSecurityPolicy(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?fleet=", nil))
	want := "default-src 'self'; connect-src 'self' ws: wss:; script-src 'self'; style-src 'self' 'unsafe-inline'"
	if got := rec.Header().Get("Content-Security-Policy"); got != want {
		t.Fatalf("want Content-Security-Policy %q, got %q", want, got)
	}
}

// TestOnlyStyleSrcIsRelaxed pins the shape of the exception rather than only its
// text. style-src carries 'unsafe-inline' for xterm.js (see the policy's own
// comment); the point of writing it down is that the keyword stays in that one
// directive. A style can corrupt how the page looks. A script can act as the
// operator, on a page that types into live sessions — and once one directive
// carries the keyword, adding it to a second one looks like consistency rather
// than like the change it is. This test is what makes that difference visible.
func TestOnlyStyleSrcIsRelaxed(t *testing.T) {
	d, _ := testDeps()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?fleet=", nil))
	policy := rec.Header().Get("Content-Security-Policy")

	for _, directive := range strings.Split(policy, ";") {
		directive = strings.TrimSpace(directive)
		name, _, _ := strings.Cut(directive, " ")
		if !strings.Contains(directive, "'unsafe-inline'") && !strings.Contains(directive, "'unsafe-eval'") {
			continue
		}
		if name != "style-src" {
			t.Errorf("%s carries an unsafe keyword: %q. Only style-src may, and only for the vendored terminal", name, directive)
		}
	}

	// Stated separately from the loop above, which would pass vacuously if a
	// directive were dropped from the policy altogether.
	if !strings.Contains(policy, "script-src 'self';") && !strings.HasSuffix(policy, "script-src 'self'") {
		t.Errorf("script-src must be exactly 'self': %q", policy)
	}
	if !strings.HasPrefix(policy, "default-src 'self';") {
		t.Errorf("default-src must be exactly 'self': %q", policy)
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

// TestEmbeddedFSContainsExpectedFiles asserts that web.FS contains exactly
// this set of files — no fewer, no more. "No fewer" catches a dropped file
// (dropping one from the go:embed line still leaves the FS non-empty, so
// TestEmbeddedFSIsNotEmpty would not catch it); "no more" catches an
// unexpected extra entry, such as a .DS_Store or editor scratch file swept in
// by an "all:" embed prefix that should not be there.
//
// This set is exact on purpose. A new file under web/ is added to this list by
// the task that adds it — don't loosen the assertion to "contains" instead of
// "equals". web/vendor/ is here because the session panel's terminal needs
// xterm.js served from the binary; the licence file beside it is not optional
// decoration, it is what makes redistributing the bundle legal, and listing it
// here is what would catch its removal.
func TestEmbeddedFSContainsExpectedFiles(t *testing.T) {
	want := map[string]bool{
		"index.html":           true,
		"setup.html":           true,
		"start.html":           true,
		"app.css":              true,
		"js/start.js":          true,
		"js/icon.js":           true,
		"js/steplist.js":       true,
		"js/store.js":          true,
		"js/main.js":           true,
		"js/api.js":            true,
		"js/i18n.js":           true,
		"js/sessions.js":       true,
		"js/fleet.js":          true,
		"js/header.js":         true,
		"js/board.js":          true,
		"js/cardnumber.js":     true,
		"js/orchestrator.js":   true,
		"js/markdown.js":       true,
		"js/envelope.js":       true,
		"js/scrollable.js":     true,
		"js/steps.js":          true,
		"js/card.js":           true,
		"js/sections.js":       true,
		"js/docs.js":           true,
		"js/session.js":        true,
		"js/theme.js":          true,
		"js/imagefile.js":      true,
		"js/pasteimage.js":     true,
		"js/pending.js":        true,
		"js/columnwidth.js":    true,
		"js/columnresize.js":   true,
		"js/buildcheck.js":     true,
		"js/liveterminal.js":   true,
		"js/terminallinks.js":  true,
		"js/update.js":         true,
		"js/terminalfont.js":   true,
		"js/fontcontrols.js":   true,
		"js/newcard.js":        true,
		"js/setup.js":          true,
		"js/lifecycle.js":      true,
		"js/initials.js":       true,
		"js/needs.js":          true,
		"vendor/xterm.js":      true,
		"vendor/xterm.css":     true,
		"vendor/LICENSE.xterm": true,
		"vendor/README.md":     true,

		// Sizes the terminal to its pane; pinned to the xterm release above.
		"vendor/addon-fit.js":      true,
		"vendor/LICENSE.addon-fit": true,
	}
	got := map[string]bool{}
	if err := fs.WalkDir(web.FS, ".", func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			got[file] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk embedded FS: %v", err)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("expected file %q missing from embedded FS", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("unexpected file %q present in embedded FS", name)
		}
	}
}

// rootRefPattern finds every reference a plain HTML or CSS file can hold to
// another file: an HTML src="..." or href="...", a CSS @import "...", or a
// CSS url(...). All three break the same way — a rename leaves the file
// referencing something that no longer exists — so all three get the same
// check. Exactly one capture group is non-empty per match; refOf picks it out.
var rootRefPattern = regexp.MustCompile(`(?:src|href)="([^"]+)"|@import\s+["']([^"']+)["']|url\(\s*["']?([^"')]+)["']?\s*\)`)

// refOf returns the single non-empty capture group of a rootRefPattern match.
func refOf(m []string) string {
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// jsImportPattern finds the specifier of every static import in a JS module:
// a plain `import x from "..."` or `import "..."`, a dynamic `import("...")`
// (the form an on-demand panel module would use to pull in xterm.js), and a
// re-export `export { x } from "..."`. It accepts both relative ("./foo.js")
// and root-relative ("/js/foo.js") specifiers — index.html already writes
// root-relative references, so a module following that same style is expected,
// not exotic.
var jsImportPattern = regexp.MustCompile(`(?:import\s*\(|import\s+(?:.*?\s+from\s+)?|export\s+.*?\s+from\s+)\s*["'](\.[^"']+|/[^"']+)["']`)

// TestStaticReferencesResolveWithinEmbeddedFS is the guard against the failure
// mode a frontend with no build step is defenceless against: nothing here
// compiles, so a renamed file leaves the Go build green and the page silently
// broken in the browser. It parses index.html for every root-relative
// src/href, and every JS module under js/ for every relative import
// specifier, and asserts each one resolves to a file that actually exists in
// the embedded FS.
func TestStaticReferencesResolveWithinEmbeddedFS(t *testing.T) {
	checkRootRefs := func(name, content string) {
		for _, m := range rootRefPattern.FindAllStringSubmatch(content, -1) {
			ref := refOf(m)
			if !strings.HasPrefix(ref, "/") {
				continue // not root-relative: e.g. a scheme'd URL, out of scope here
			}
			target := strings.TrimPrefix(ref, "/")
			if _, err := fs.Stat(web.FS, target); err != nil {
				t.Errorf("%s references %q, which does not exist in the embedded FS", name, ref)
			}
		}
	}

	index, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	checkRootRefs("index.html", string(index))

	setupPage, err := fs.ReadFile(web.FS, "setup.html")
	if err != nil {
		t.Fatalf("read setup.html: %v", err)
	}
	checkRootRefs("setup.html", string(setupPage))

	css, err := fs.ReadFile(web.FS, "app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	checkRootRefs("app.css", string(css))

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
			var target string
			if strings.HasPrefix(spec, "/") {
				target = strings.TrimPrefix(spec, "/")
			} else {
				target = path.Join(path.Dir(file), spec)
			}
			if _, err := fs.Stat(web.FS, target); err != nil {
				t.Errorf("%s imports %q, which resolves to %q and does not exist in the embedded FS", file, spec, target)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk js/: %v", err)
	}
}
