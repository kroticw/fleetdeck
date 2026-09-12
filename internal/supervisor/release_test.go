package supervisor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The releases page answers /releases/latest with a redirect to the newest
// tag. Reading the tag out of that redirect is what keeps this off the GitHub
// API, whose anonymous allowance is sixty calls an hour for a whole address
// and was already spent on the operator's machine when this was written.
func TestLatestReadsTheTagOutOfTheRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/kroticw/fleetdeck/releases/latest" {
			t.Errorf("asked for %s, not the latest release", r.URL.Path)
		}
		http.Redirect(w, r, "/kroticw/fleetdeck/releases/tag/v0.4.0", http.StatusFound)
	}))
	defer srv.Close()

	tag, err := (&Releases{Base: srv.URL + "/kroticw/fleetdeck"}).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.4.0" {
		t.Fatalf("read tag %q, want v0.4.0", tag)
	}
}

// Following the redirect would download the whole release page for a string
// that is already in the Location header.
func TestLatestDoesNotFetchTheReleasePage(t *testing.T) {
	var pageHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/releases/tag/") {
			pageHits.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/releases/tag/v0.4.0", http.StatusFound)
	}))
	defer srv.Close()

	if _, err := (&Releases{Base: srv.URL}).Latest(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := pageHits.Load(); n != 0 {
		t.Fatalf("the release page was fetched %d time(s); the tag is in the redirect", n)
	}
}

// No network is the commonest reason an update cannot happen, and the one a
// person is likeliest to be able to do something about. It has to arrive as
// something the window can name, not as a transport error nobody reads.
func TestLatestSaysTheReleasesPageCouldNotBeReached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close() // nothing answers there now

	_, err := (&Releases{Base: addr}).Latest(context.Background())
	var unreachable *ReleasesUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("got %v, want a *ReleasesUnreachableError", err)
	}
	if !strings.Contains(unreachable.Error(), addr) {
		t.Errorf("the error does not say where it tried to look: %v", unreachable)
	}
}

// GitHub answering anything but a redirect means the shape this code reads has
// changed, or something between here and there is answering instead.
func TestLatestRefusesAnAnswerThatIsNotARedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := (&Releases{Base: srv.URL}).Latest(context.Background())
	if err == nil {
		t.Fatal("a 200 was accepted as an answer about the latest release")
	}
	if !strings.Contains(err.Error(), "200") {
		t.Errorf("the error does not say what was answered: %v", err)
	}
}

// A redirect to somewhere that is not a release tag -- a login page, say, or
// a repository that has no releases at all.
func TestLatestRefusesALocationThatIsNotAReleaseTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login?return_to=%2Freleases", http.StatusFound)
	}))
	defer srv.Close()

	_, err := (&Releases{Base: srv.URL}).Latest(context.Background())
	if err == nil {
		t.Fatal("a redirect to a login page was read as a release tag")
	}
}

// A repository with no releases at all redirects to the releases index rather
// than to a tag, and that is a different thing to say than "the page broke".
func TestLatestSaysThereAreNoReleasesYet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/kroticw/fleetdeck/releases", http.StatusFound)
	}))
	defer srv.Close()

	_, err := (&Releases{Base: srv.URL + "/kroticw/fleetdeck"}).Latest(context.Background())
	var none *NoReleasesError
	if !errors.As(err, &none) {
		t.Fatalf("got %v, want a *NoReleasesError", err)
	}
}

// The archive's name is a contract with scripts/build-dist-app.sh, which
// writes fleetdeck-<version>-macos.zip. TestDistAppBuildsAnAppAPersonCanInstall
// in cmd/fleetdeck-window holds the two spellings together: it names the zip in
// Go and then requires the real `make dist-app` to leave that exact path.
func TestArchiveURLNamesTheZipTheReleaseCarries(t *testing.T) {
	got := (&Releases{Base: "https://github.com/kroticw/fleetdeck"}).ArchiveURL("v0.4.0")
	want := "https://github.com/kroticw/fleetdeck/releases/download/v0.4.0/fleetdeck-v0.4.0-macos.zip"
	if got != want {
		t.Fatalf("archive URL %q, want %q", got, want)
	}
}

func TestNewerCompares(t *testing.T) {
	cases := []struct {
		name       string
		have, want string
		newer      bool
	}{
		{"a later minor is newer", "v0.3.0", "v0.4.0", true},
		{"the same version is not newer", "v0.3.0", "v0.3.0", false},
		{"an earlier version is not newer", "v0.4.0", "v0.3.0", false},
		// The one comparison a string comparison gets wrong, and the reason
		// this is not strings.Compare: "v0.10.0" < "v0.9.0" as text.
		{"ten is newer than nine", "v0.9.0", "v0.10.0", true},
		{"a later patch is newer", "v0.4.0", "v0.4.1", true},
		{"a later major is newer", "v0.9.9", "v1.0.0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Newer(c.have, c.want)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.newer {
				t.Fatalf("Newer(%q, %q) = %v, want %v", c.have, c.want, got, c.newer)
			}
		})
	}
}

// A build from a checkout reports "dev" (internal/version), and there is no
// answering whether a release is newer than that. Saying so is the point:
// the window shows a build with no version a different refusal.
func TestNewerRefusesAVersionItCannotRead(t *testing.T) {
	for _, v := range []string{"dev", "", "v1.2", "v1.2.x", "1.2.3-rc1-and-then-some"} {
		if _, err := Newer(v, "v0.4.0"); err == nil {
			t.Errorf("Newer(%q, ...) read it as a version", v)
		}
	}
}
