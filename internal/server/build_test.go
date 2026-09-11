package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/buildinfo"
	"github.com/kroticw/fleetdeck/web"
)

// The page learns which build it came from out of the same response that
// delivered it, not out of the first snapshot on the socket. The panel is
// replaced under a live window; a page that took its identity from the first
// snapshot would, if the swap landed between the two, adopt the new panel's
// fingerprint while running the old panel's code -- and never see the
// mismatch it exists to see.

const testWebHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func depsWithBuild() Deps {
	d, _ := testDeps()
	d.Build = &buildinfo.Fingerprint{Web: testWebHash, Revision: "24d0c7a"}
	return d
}

func get(t *testing.T, d Deps, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexCarriesTheBuildFingerprintInsideItsHead(t *testing.T) {
	rec := get(t, depsWithBuild(), "/")
	body := rec.Body.String()

	meta := `<meta name="fleetdeck-build" content="` + testWebHash + `">`
	at := strings.Index(body, meta)
	if at < 0 {
		t.Fatalf("index.html does not carry the fingerprint:\n%s", body)
	}
	if head := strings.Index(body, "</head>"); head < 0 || at > head {
		t.Fatal("the fingerprint landed outside <head>, where the page does not look for it")
	}
}

// The real index.html, not a stand-in: the fingerprint goes in at its <head>
// tag, and an edit to that tag -- an attribute added, say -- would quietly
// stop the injection with every test above still passing on a fixture.
func TestTheRealIndexStillHasThePlaceTheFingerprintGoes(t *testing.T) {
	data, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), headTag); n != 1 {
		t.Fatalf("index.html has %d %q tags, want exactly one: the fingerprint is inserted after it", n, headTag)
	}
}

// A document served from cache after "reload" would carry the old
// fingerprint, the banner would come straight back, and the button would be
// seen not to keep the promise written on it. The index is a couple of
// kilobytes over loopback; asking for it afresh costs nothing.
func TestIndexIsNeverTakenFromCacheWithoutAsking(t *testing.T) {
	rec := get(t, depsWithBuild(), "/")
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

func TestIndexKeepsItsSecurityPolicyWhenRewritten(t *testing.T) {
	rec := get(t, depsWithBuild(), "/")
	if rec.Header().Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Fatal("the rewritten index lost its Content-Security-Policy")
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
}

// Only the document is rewritten. A stylesheet or a module comes through
// byte for byte, fingerprint or not.
func TestOtherFilesAreServedUntouched(t *testing.T) {
	want, err := fs.ReadFile(web.FS, "app.css")
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, depsWithBuild(), "/app.css")
	if rec.Body.String() != string(want) {
		t.Fatal("app.css was altered on the way out")
	}
}

// A panel wired without a fingerprint -- every test in this package that
// predates it, for one -- serves the page exactly as embedded. The page then
// has nothing to compare and shows no banner, which is how it behaved before.
func TestIndexWithoutAFingerprintIsServedAsEmbedded(t *testing.T) {
	d, _ := testDeps()
	want, err := fs.ReadFile(web.FS, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	rec := get(t, d, "/")
	if rec.Body.String() != string(want) {
		t.Fatal("index.html changed with no fingerprint to put in it")
	}
}

func TestSnapshotCarriesTheBuild(t *testing.T) {
	rec := get(t, depsWithBuild(), "/api/snapshot")
	var snap struct {
		Build *buildinfo.Fingerprint `json:"build"`
		Cards []json.RawMessage      `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Build == nil || snap.Build.Web != testWebHash {
		t.Fatalf("snapshot build = %+v, want web %s", snap.Build, testWebHash)
	}
	// Stamping the build must not cost the snapshot what it already carried.
	if len(snap.Cards) != 1 {
		t.Fatalf("the snapshot lost its cards: %s", rec.Body.String())
	}
}

// The socket is where a page open for hours learns that the panel under it
// has changed; the HTTP route is only its first load.
func TestWebSocketPushCarriesTheBuild(t *testing.T) {
	d := wsDeps()
	d.Build = &buildinfo.Fingerprint{Web: testWebHash}
	d.interval = 10 * time.Second
	url, _ := wsServer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	snap := readSnapshot(ctx, t, conn)
	if snap.Build == nil || snap.Build.Web != testWebHash {
		t.Fatalf("pushed build = %+v, want web %s", snap.Build, testWebHash)
	}
}
