package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docsDeps returns a Deps whose documentation roots are the ones given. Every
// other dependency stays wired to testDeps' recorders, so a documentation test
// exercises the same router the rest of the API is served by.
func docsDeps(t *testing.T, roots ...string) Deps {
	t.Helper()
	d, _ := testDeps()
	d.DocsRoots = roots
	return d
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func getDocs(d Deps, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func contentURL(path string) string {
	return "/api/docs/content?path=" + url.QueryEscape(path)
}

func TestDocsListsMarkdownOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "# a")
	writeFile(t, filepath.Join(dir, "b.png"), "x")

	rec := getDocs(docsDeps(t, dir), "/api/docs")
	body := rec.Body.String()
	if !strings.Contains(body, "a.md") || strings.Contains(body, "b.png") {
		t.Fatalf("only markdown belongs in the docs list: %s", body)
	}
}

func TestDocsRefusesPathsOutsideRoots(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "# a")

	rec := getDocs(docsDeps(t, dir), contentURL(filepath.Join(dir, "..", "etc", "passwd")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a path outside the configured roots must be refused, got %d", rec.Code)
	}
}

func TestDocsWithNoRootsConfiguredSaysSo(t *testing.T) {
	rec := getDocs(docsDeps(t), "/api/docs")
	if rec.Code == http.StatusOK && rec.Body.String() == "[]\n" {
		t.Fatal("no configured roots must be stated, not shown as an empty documentation set")
	}
}

func TestDocsContentReturnsTheDocument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "# a\n\nbody text")

	rec := getDocs(docsDeps(t, dir), contentURL(filepath.Join(dir, "a.md")))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Path string `json:"path"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if got.Body != "# a\n\nbody text" {
		t.Fatalf("the document must be returned verbatim, got %q", got.Body)
	}
}

// The list carries a relative title and an absolute path, and nesting is walked:
// a docs directory with subdirectories is the normal case, not the exception.
func TestDocsWalksNestedDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "guide", "install.md"), "# install")

	rec := getDocs(docsDeps(t, dir), "/api/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var docs []Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(docs) != 1 {
		t.Fatalf("want one nested document, got %d: %s", len(docs), rec.Body.String())
	}
	if docs[0].Title != filepath.Join("guide", "install.md") {
		t.Fatalf("the title must be the path relative to its root, got %q", docs[0].Title)
	}
	if !filepath.IsAbs(docs[0].Path) {
		t.Fatalf("the path must be absolute so the content route can be called with it, got %q", docs[0].Path)
	}
}

// A configured root that holds no markdown is an empty documentation set, and an
// empty set must arrive as an empty JSON array rather than null: the panel maps
// over what this route returns, and null is not something to map over.
func TestDocsWithRootsButNoMarkdownIsAnEmptyArray(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "b.png"), "x")

	rec := getDocs(docsDeps(t, dir), "/api/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("an empty documentation set must be an empty array, got %s", rec.Body.String())
	}
}

// Spec section 8: a path arriving from outside is confined to the configured
// directories, and both sides are resolved through their symlinks. A markdown
// symlink sitting inside a documentation root and pointing out of it is the
// shape a prefix test lets through — it must not be served.
func TestDocsRefusesSymlinkOutOfRoots(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	writeFile(t, secret, "# secret")
	link := filepath.Join(dir, "escape.md")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	rec := getDocs(docsDeps(t, dir), contentURL(link))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a symlink leaving the configured roots must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("the refusal must not carry the linked document: %s", rec.Body.String())
	}
}

// The same escape must not be advertised either: a document listed but refused
// on open is a broken link the operator cannot explain.
func TestDocsListOmitsSymlinksOutOfRoots(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.md"), "# secret")
	writeFile(t, filepath.Join(dir, "a.md"), "# a")
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(dir, "escape.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	rec := getDocs(docsDeps(t, dir), "/api/docs")
	var docs []Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	// Checked by where each listed document resolves to, not by the link's own
	// name: the name is gone once the link is resolved, so a test looking for
	// "escape.md" would pass whether or not the linked file was listed.
	if len(docs) != 1 {
		t.Fatalf("only the document inside the root belongs in the list, got %d: %s", len(docs), rec.Body.String())
	}
	if filepath.Base(docs[0].Path) != "a.md" {
		t.Fatalf("a document that leaves the configured roots must not be listed: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("nothing outside the roots may appear in the list: %s", rec.Body.String())
	}
}

// A directory whose name merely begins with a root's is not inside it. Compared
// as a bare string prefix, /docs-private next to /docs passes for a child.
func TestDocsRefusesSiblingDirectorySharingTheRootsPrefix(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "docs")
	sibling := filepath.Join(parent, "docs-private")
	writeFile(t, filepath.Join(root, "a.md"), "# a")
	writeFile(t, filepath.Join(sibling, "secret.md"), "# secret")

	rec := getDocs(docsDeps(t, root), contentURL(filepath.Join(sibling, "secret.md")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a sibling directory sharing the root's prefix is not inside it, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The other direction of the same rule. On macOS t.TempDir() lives under /var,
// which is itself a symlink to /private/var: resolving only the requested path
// and not the root makes a document that is plainly inside its root look foreign.
func TestDocsServesDocumentUnderASymlinkedRoot(t *testing.T) {
	realDir := t.TempDir()
	writeFile(t, filepath.Join(realDir, "a.md"), "# a")
	linkedRoot := filepath.Join(t.TempDir(), "docs-link")
	if err := os.Symlink(realDir, linkedRoot); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// The root is configured through the symlink and the document is asked for
	// by its real path: one side resolved and the other not is exactly the
	// mismatch this must survive.
	rec := getDocs(docsDeps(t, linkedRoot), contentURL(filepath.Join(realDir, "a.md")))
	if rec.Code != http.StatusOK {
		t.Fatalf("a document inside a symlinked root must be served, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A relative path is taken as relative to a configured root, so climbing out of
// one with .. is the same refusal as naming somewhere else outright.
func TestDocsRefusesRelativeEscape(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "# a")

	rec := getDocs(docsDeps(t, dir), contentURL("../../../../etc/passwd"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a relative path climbing out of the roots must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A relative path that stays inside is served: the refusal above is about where
// the path lands, not about the path being relative.
func TestDocsServesRelativePathInsideARoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "guide", "install.md"), "# install")

	rec := getDocs(docsDeps(t, dir), contentURL(filepath.Join("guide", "install.md")))
	if rec.Code != http.StatusOK {
		t.Fatalf("a relative path inside a root must be served, got %d: %s", rec.Code, rec.Body.String())
	}
}

// This route serves the documentation section, and the section is markdown. A
// file that merely shares the directory — an .env, a private key an operator
// keeps next to their notes — is not documentation and is not served.
func TestDocsRefusesNonMarkdownInsideARoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), "TOKEN=secret")

	rec := getDocs(docsDeps(t, dir), contentURL(filepath.Join(dir, ".env")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("only markdown is served from a documentation root, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("the refusal must not carry the file's content: %s", rec.Body.String())
	}
}

// A document inside a root that is not there is a 404, not a 403: the operator
// is looking at a stale list, not at an attempt to leave the roots.
func TestDocsMissingDocumentIsNotFound(t *testing.T) {
	dir := t.TempDir()

	rec := getDocs(docsDeps(t, dir), contentURL(filepath.Join(dir, "gone.md")))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a missing document inside a root must be 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The content route needs roots for the same reason the list does: without them
// there is nothing to confine a path to, and a path from outside must never be
// read unconfined.
// It says which of the two it is, too: "nothing is configured" and "that is
// outside what is configured" send the operator to different places.
func TestDocsContentWithNoRootsConfiguredIsRefused(t *testing.T) {
	rec := getDocs(docsDeps(t), contentURL("/etc/passwd"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("with no roots configured nothing can be served, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no documentation roots are configured") {
		t.Fatalf("the refusal must say the roots are unconfigured, not that the path is outside them: %s", rec.Body.String())
	}
}

// An empty path names no document. It must be refused rather than resolved into
// a configured root, which is where an empty relative path would otherwise land.
func TestDocsContentWithoutAPathIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "# a")

	rec := getDocs(docsDeps(t, dir), "/api/docs/content")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a content request naming no document must be refused, got %d: %s", rec.Code, rec.Body.String())
	}
}

// Every configured root contributes, and each document says which one it came
// from: two roots is the shape the configuration key allows, and the panel has
// to show them as one list.
func TestDocsListsEveryConfiguredRoot(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(first, "one.md"), "# one")
	writeFile(t, filepath.Join(second, "two.md"), "# two")

	rec := getDocs(docsDeps(t, first, second), "/api/docs")
	var docs []Doc
	if err := json.Unmarshal(rec.Body.Bytes(), &docs); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(docs) != 2 {
		t.Fatalf("want a document from each root, got %d: %s", len(docs), rec.Body.String())
	}
	if docs[0].Root == docs[1].Root {
		t.Fatalf("each document must name the root it came from: %s", rec.Body.String())
	}
}

// A configured root that is not there is a configuration mistake, and saying
// "no documentation" instead of naming it sends the operator looking for the
// wrong problem. When nothing configured can be read at all, the route says so.
func TestDocsRootThatCannotBeReadIsStated(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")

	rec := getDocs(docsDeps(t, missing), "/api/docs")
	if rec.Code == http.StatusOK {
		t.Fatalf("an unreadable root must not pass as an empty documentation set, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not-there") {
		t.Fatalf("the refusal must name the root it could not read: %s", rec.Body.String())
	}
}

// One unreadable root among several does not hide the ones that work.
func TestDocsListsReadableRootsAlongsideAnUnreadableOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.md"), "# a")
	missing := filepath.Join(t.TempDir(), "not-there")

	rec := getDocs(docsDeps(t, missing, dir), "/api/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "a.md") {
		t.Fatalf("a readable root must still be listed: %s", rec.Body.String())
	}
}

// The documentation routes are read-only. A write reaching them is a 405 from
// the router, not a handler quietly treating it as a read.
func TestDocsRoutesAreReadOnly(t *testing.T) {
	dir := t.TempDir()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/docs", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	New(docsDeps(t, dir)).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("the documentation routes read only, got %d: %s", rec.Code, rec.Body.String())
	}
}
