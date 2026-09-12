package supervisor

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Releases is the repository's releases page, as the window asks it what the
// newest version is.
//
// It asks over the plain web address, not the GitHub API, and that is a
// decision with a measurement behind it: the API allows an unauthenticated
// address sixty calls an hour, and on the operator's own machine that
// allowance was already spent -- by other tooling on the same address -- when
// this was written. `/releases/latest` answers every caller with a 302 whose
// Location carries the newest tag, and the tag is the whole answer.
type Releases struct {
	// Base is the repository's web address, without a trailing slash:
	// https://github.com/kroticw/fleetdeck.
	Base string
	// Client is the HTTP client to ask with; nil means one with LatestTimeout.
	Client *http.Client
}

// LatestTimeout bounds the one request Latest makes. It is short on purpose:
// this runs behind a button a person is waiting at, and a network that is
// there but not answering has to become a refusal quickly enough to read.
const LatestTimeout = 15 * time.Second

// ReleasesUnreachableError: the releases page could not be asked at all --
// no network, no name resolution, nothing listening, a timeout. It is the
// commonest reason an update cannot happen and the one a person can act on,
// so it is a type the window can recognise rather than a wrapped transport
// error.
type ReleasesUnreachableError struct {
	URL string
	Err error
}

func (e *ReleasesUnreachableError) Error() string {
	return fmt.Sprintf("could not reach %s: %v", e.URL, e.Err)
}

func (e *ReleasesUnreachableError) Unwrap() error { return e.Err }

// NoReleasesError: the repository has no published release. GitHub redirects
// /releases/latest to the releases index in that case rather than to a tag.
type NoReleasesError struct{ URL string }

func (e *NoReleasesError) Error() string {
	return fmt.Sprintf("%s has published no release yet", e.URL)
}

// Latest is the tag of the newest published release.
func (r *Releases) Latest(ctx context.Context) (string, error) {
	latest := r.Base + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latest, nil)
	if err != nil {
		return "", err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return "", &ReleasesUnreachableError{URL: latest, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("%s answered %d, not a redirect to the newest release", latest, resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("%s redirected without saying where", latest)
	}
	tag, err := tagInLocation(location)
	if err != nil {
		return "", fmt.Errorf("%s: %w", latest, err)
	}
	if tag == "" {
		return "", &NoReleasesError{URL: r.Base}
	}
	return tag, nil
}

// ArchiveURL is where the app for tag is downloaded from. The name is the one
// scripts/build-dist-app.sh writes, and a test in cmd/fleetdeck-window holds
// the two spellings together.
func (r *Releases) ArchiveURL(tag string) string {
	return fmt.Sprintf("%s/releases/download/%s/%s", r.Base, tag, ArchiveName(tag))
}

// ArchiveName is the release app's file name for tag.
func ArchiveName(tag string) string {
	return "fleetdeck-" + tag + "-macos.zip"
}

// client is a client that does not follow redirects: the answer is the
// redirect, and following it would download a web page for a string already
// in its header.
func (r *Releases) client() *http.Client {
	c := r.Client
	if c == nil {
		c = &http.Client{Timeout: LatestTimeout}
	}
	redirectless := *c
	redirectless.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &redirectless
}

// tagInLocation reads the tag out of a /releases/tag/<tag> path. It returns
// "" with no error for a redirect to the releases index, which is what a
// repository with no releases answers.
func tagInLocation(location string) (string, error) {
	u, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("redirected to %q, which is not an address", location)
	}
	path := strings.TrimSuffix(u.Path, "/")
	if _, rest, found := strings.Cut(path, "/releases/tag/"); found {
		if rest == "" || strings.Contains(rest, "/") {
			return "", fmt.Errorf("redirected to %q, which names no single tag", location)
		}
		return rest, nil
	}
	if strings.HasSuffix(path, "/releases") {
		return "", nil
	}
	return "", fmt.Errorf("redirected to %q, which is not a release", location)
}

// Newer says whether want is a later version than have. Both must be release
// tags, vX.Y.Z; anything else -- "dev", an empty string, a two-part version --
// is refused rather than guessed at, because guessing here decides whether an
// app replaces itself.
func Newer(have, want string) (bool, error) {
	h, err := parseTag(have)
	if err != nil {
		return false, err
	}
	w, err := parseTag(want)
	if err != nil {
		return false, err
	}
	for i := range h {
		if w[i] != h[i] {
			return w[i] > h[i], nil
		}
	}
	return false, nil
}

// parseTag reads vX.Y.Z into its three numbers. Compared as numbers and never
// as text: as text "v0.10.0" sorts before "v0.9.0", and an update that
// compared them that way would refuse to move off v0.9.0 forever.
func parseTag(tag string) ([3]int, error) {
	var v [3]int
	rest, found := strings.CutPrefix(tag, "v")
	if !found {
		return v, fmt.Errorf("%q is not a release version (they look like v1.2.3)", tag)
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("%q is not a release version (they look like v1.2.3)", tag)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("%q is not a release version (they look like v1.2.3)", tag)
		}
		v[i] = n
	}
	return v, nil
}
