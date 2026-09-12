//go:build darwin

package supervisor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// zipOf packs a bundle the way scripts/build-dist-app.sh packs a release --
// ditto, no resource forks, no extended attributes, no ACLs -- and serves the
// bytes. A signature survives that round trip; docs/engineering/release-app.md
// section 3 measured it through every unpacker on this machine.
func zipOf(t *testing.T, bundle string) []byte {
	t.Helper()
	zip := filepath.Join(t.TempDir(), "release.zip")
	out, err := exec.Command("/usr/bin/ditto",
		"-c", "-k", "--norsrc", "--noextattr", "--noacl", "--keepParent", bundle, zip).CombinedOutput()
	if err != nil {
		t.Fatalf("pack %s: %v\n%s", bundle, err, out)
	}
	data, err := os.ReadFile(zip)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// releaseZip packs a bundle under the name a release archive carries it by.
// A release holds fleetdeck.app and nothing else, and the name is part of what
// Stage checks; the bundles these tests build are named for what they are.
// Renaming the directory does not touch the signature, which covers the
// bundle's contents and not its path.
func releaseZip(t *testing.T, bundle string) []byte {
	t.Helper()
	named := filepath.Join(t.TempDir(), BundleName)
	if out, err := exec.Command("/usr/bin/ditto", bundle, named).CombinedOutput(); err != nil {
		t.Fatalf("name %s as %s: %v\n%s", bundle, BundleName, err, out)
	}
	return zipOf(t, named)
}

// serveRelease stands in for the releases page and its downloads: the redirect
// that names the newest tag, and the archive itself.
func serveRelease(t *testing.T, tag string, archive []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+ArchiveName(tag), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// runningVersion is the version the app doing the updating reports in these
// tests: the one the operator actually had installed when this was written.
const runningVersion = "v0.3.0"

func releaseSource(t *testing.T, srv *httptest.Server, teamID string) *ReleaseSource {
	t.Helper()
	return &ReleaseSource{
		Releases: &Releases{Base: srv.URL},
		Seal:     realSeal(teamID),
		Ditto:    "/usr/bin/ditto",
		Running:  runningVersion,
	}
}

// The whole path, on the real signed app: ask what is newest, download it,
// unpack it, check it, and hand back a bundle ready to be started.
func TestStageDownloadsUnpacksAndChecksTheRelease(t *testing.T) {
	app := developerIDBundle(t)
	srv := serveRelease(t, "v9.9.9", releaseZip(t, app))
	src := releaseSource(t, srv, teamIDOfInstalledApp(t))
	dir := t.TempDir()

	staged, err := src.Stage(context.Background(), dir, "v9.9.9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(staged) != dir {
		t.Errorf("staged at %s, want it inside %s", staged, dir)
	}
	if _, err := os.Stat(PanelIn(staged)); err != nil {
		t.Errorf("the staged bundle has no panel where the window looks for one: %v", err)
	}
}

// The control the whole feature is for: an archive that is not signed the way
// a release is must not become the app. An ad-hoc bundle is what anybody can
// build and sign, and it is served here over real HTTP -- the download is
// real, only the archive is a forgery.
func TestStageRefusesAnArchiveThatIsNotSignedLikeARelease(t *testing.T) {
	srv := serveRelease(t, "v9.9.9", releaseZip(t, adhocBundle(t)))
	src := releaseSource(t, srv, "PTLLPQ8LY4")

	_, err := src.Stage(context.Background(), t.TempDir(), "v9.9.9", nil)

	var sealErr *SealError
	if !errors.As(err, &sealErr) {
		t.Fatalf("got %v, want a *SealError", err)
	}
	if sealErr.Reason != SealNotDeveloperID {
		t.Fatalf("refused for %q, want %q", sealErr.Reason, SealNotDeveloperID)
	}
}

// A refused archive must leave nothing behind that could be started later, by
// this program or by a person who finds it in Finder.
func TestStageLeavesNothingBehindWhenItRefuses(t *testing.T) {
	srv := serveRelease(t, "v9.9.9", releaseZip(t, adhocBundle(t)))
	src := releaseSource(t, srv, "PTLLPQ8LY4")
	dir := t.TempDir()

	if _, err := src.Stage(context.Background(), dir, "v9.9.9", nil); err == nil {
		t.Fatal("the forged archive was accepted")
	}

	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		names := make([]string, 0, len(left))
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Fatalf("a refused download left %s behind", strings.Join(names, ", "))
	}
}

func TestStageSaysSoWhenTheArchiveIsNotThere(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	src := releaseSource(t, srv, "PTLLPQ8LY4")

	_, err := src.Stage(context.Background(), t.TempDir(), "v9.9.9", nil)
	if err == nil {
		t.Fatal("a 404 was not reported")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("the error does not say what the server answered: %v", err)
	}
}

// A download with no bound on it is a way to fill somebody's disk from
// somewhere else. The release app is around 40 MB; the bound is generous and
// it exists.
func TestStageStopsADownloadThatWillNotEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for range 64 {
			if _, err := w.Write(make([]byte, 1<<20)); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	src := releaseSource(t, srv, "PTLLPQ8LY4")
	src.MaxArchiveBytes = 1 << 20

	_, err := src.Stage(context.Background(), t.TempDir(), "v9.9.9", nil)
	if err == nil {
		t.Fatal("an endless download was accepted")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("the error does not say the download was too large: %v", err)
	}
}

// An archive holding something other than the app -- two bundles, a directory
// of files, an app under another name -- is not a release, whatever it is.
func TestStageRefusesAnArchiveThatIsNotTheApp(t *testing.T) {
	odd := filepath.Join(t.TempDir(), "something-else")
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(odd, "readme"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := serveRelease(t, "v9.9.9", zipOf(t, odd))
	src := releaseSource(t, srv, "PTLLPQ8LY4")

	_, err := src.Stage(context.Background(), t.TempDir(), "v9.9.9", nil)
	if err == nil {
		t.Fatal("an archive with no app in it was accepted")
	}
	if !strings.Contains(err.Error(), "fleetdeck.app") {
		t.Errorf("the error does not say what was missing: %v", err)
	}
}

// Not enough room is a refusal a person can act on, and it has to come before
// the download rather than as a write failing half way through.
func TestStageRefusesBeforeDownloadingWhenThereIsNoRoom(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte("never read"))
	}))
	defer srv.Close()
	src := releaseSource(t, srv, "PTLLPQ8LY4")
	src.NeedBytes = 1 << 60 // an exabyte

	_, err := src.Stage(context.Background(), t.TempDir(), "v9.9.9", nil)

	var room *NoRoomError
	if !errors.As(err, &room) {
		t.Fatalf("got %v, want a *NoRoomError", err)
	}
	if hits != 0 {
		t.Errorf("the archive was fetched %d time(s) before the room was checked", hits)
	}
}

// Check is the question the button asks first, and its three answers are: a
// newer version, this one, and cannot tell.
func TestCheckReportsANewerRelease(t *testing.T) {
	srv := serveRelease(t, "v0.4.0", nil)
	src := releaseSource(t, srv, "PTLLPQ8LY4")

	tag, err := src.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.4.0" {
		t.Fatalf("Check said %q, want v0.4.0", tag)
	}
}

func TestCheckReportsNothingNewWhenTheReleaseIsTheRunningOne(t *testing.T) {
	srv := serveRelease(t, "v0.3.0", nil)
	src := releaseSource(t, srv, "PTLLPQ8LY4")

	tag, err := src.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "" {
		t.Fatalf("Check said %q, want nothing to update to", tag)
	}
}

// An older release than the running one means somebody has unpublished a
// version, or is answering for GitHub. Either way it is not an update, and
// going back to it silently would be the worst of the three answers.
func TestCheckRefusesToGoBackwards(t *testing.T) {
	srv := serveRelease(t, "v0.2.0", nil)
	src := releaseSource(t, srv, "PTLLPQ8LY4")

	tag, err := src.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "" {
		t.Fatalf("Check offered %q, which is older than the running v0.3.0", tag)
	}
}
