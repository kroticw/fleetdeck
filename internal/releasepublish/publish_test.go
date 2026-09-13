package releasepublish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const tag = "v1.2.3"

// artifacts writes the files a release build leaves in dist/ and returns their
// paths in the order a shell glob would pass them.
func artifacts(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func build(t *testing.T) (map[string]string, []string) {
	t.Helper()
	files := map[string]string{
		"app-" + tag + "-darwin-amd64.tar.gz": "amd64 archive, this build",
		"app-" + tag + "-darwin-arm64.tar.gz": "arm64 archive, this build",
		"app-" + tag + "-macos.zip":           "zipped app, this build",
		"app-" + tag + "-macos.dmg":           "disk image, this build",
	}
	return files, artifacts(t, files)
}

// requirePublished is the acceptance of a release, checked on the fake server's own
// state rather than on anything the publisher said: exactly one release carries the
// tag and it is published, it has notes, and it holds every artifact of this build
// byte for byte and nothing half uploaded.
func requirePublished(t *testing.T, fg *fakeGitHub, files map[string]string) *fakeRelease {
	t.Helper()
	var published []*fakeRelease
	for _, rel := range fg.releasesForTag() {
		if !rel.Draft {
			published = append(published, rel)
		}
	}
	if len(published) != 1 {
		t.Fatalf("%d published releases carry %s, want exactly one", len(published), tag)
	}
	rel := published[0]
	if rel.Body == "" || rel.Name == "" {
		t.Errorf("the release has no generated title or notes: name %q, body %q", rel.Name, rel.Body)
	}
	got := map[string]string{}
	for _, a := range rel.Assets {
		if a.State != "uploaded" {
			t.Errorf("asset %s is left in state %q", a.Name, a.State)
		}
		if _, dup := got[a.Name]; dup {
			t.Errorf("asset %s is attached twice", a.Name)
		}
		got[a.Name] = string(a.Data)
	}
	for name, want := range files {
		if got[name] != want {
			t.Errorf("asset %s holds %q, want %q", name, got[name], want)
		}
	}
	if len(got) != len(files) {
		t.Errorf("the release holds %d assets, want %d", len(got), len(files))
	}
	return rel
}

// The path every release took until now: nothing exists, so the release is made,
// filled and published in one pass.
func TestPublishCreatesAReleaseThatDoesNotExist(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	if n := len(fg.releasesForTag()); n != 1 {
		t.Errorf("%d releases carry the tag, want one", n)
	}
}

// Browsers and the updater are told what a file is by the content type it was
// uploaded with. The values are the ones every release so far carried.
func TestPublishUploadsEachArtifactWithTheContentTypeReleasesAlreadyUse(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		".tar.gz": "application/x-gtar",
		".zip":    "application/zip",
		".dmg":    "application/x-apple-diskimage",
	}
	for _, a := range fg.releasesForTag()[0].Assets {
		for ext, ct := range want {
			if strings.HasSuffix(a.Name, ext) && a.ContentType != ct {
				t.Errorf("%s uploaded as %q, want %q", a.Name, a.ContentType, ct)
			}
		}
	}
}

// v0.8.0: the attempts to publish by hand answered with errors and still left two
// empty drafts on the tag. The retry must finish the release with one of them rather
// than stop, and must not add a third.
func TestPublishFinishesALeftoverEmptyDraftInsteadOfCreatingAnother(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	first := fg.addRelease(tag, true, "", nil)
	fg.addRelease(tag, true, "", nil)
	var log bytes.Buffer

	if err := fg.publisher(&log, 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	rel := requirePublished(t, fg, files)
	if rel.ID != first.ID {
		t.Errorf("published release %d, want the oldest draft %d", rel.ID, first.ID)
	}
	if n := len(fg.releasesForTag()); n != 2 {
		t.Errorf("%d releases carry the tag, want the two that were there", n)
	}
	if !strings.Contains(log.String(), "::warning::") {
		t.Errorf("the other draft on the tag went unmentioned; log:\n%s", log.String())
	}
}

// A draft left by an earlier run holds that run's files. Nobody has downloaded
// them — it is a draft — so this build's files replace them.
func TestPublishReplacesArtifactsAnEarlierRunLeftOnADraft(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.addRelease(tag, true, generatedNotes(tag), map[string]string{
		"app-" + tag + "-darwin-amd64.tar.gz": "amd64 archive, earlier build",
		"app-" + tag + "-macos.zip":           "zipped app, earlier build",
	})

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
}

// An asset GitHub already holds with exactly these bytes is not sent again: a
// dmg is tens of megabytes, and a retry after a failed publish should not pay for
// uploads that already landed.
func TestPublishDoesNotUploadAgainWhatIsAlreadyThere(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.addRelease(tag, true, generatedNotes(tag), files)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	for _, m := range fg.mutations() {
		if strings.Contains(m, "/assets") {
			t.Errorf("unexpected asset request %s", m)
		}
	}
}

// GitHub documents that a 502 during an upload can leave an empty asset in state
// "starter" behind. It holds the name, so it has to go before the file is sent again.
func TestPublishClearsAHalfMadeUpload(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "upload", times: 1, status: http.StatusBadGateway, starter: true})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
}

// The 2026-09-13 failure itself: the create answered 500 and made the release
// anyway. Sending the create again would make a second one; the retry has to look
// first.
func TestPublishDoesNotCreateTwiceWhenACreateFailedButHappened(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "create", times: 1, status: http.StatusInternalServerError, after: true})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	if n := len(fg.releasesForTag()); n != 1 {
		t.Errorf("%d releases carry the tag, want one", n)
	}
}

// An upload that answered 502 after it stored the file.
func TestPublishSurvivesAnUploadThatFailedButHappened(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "upload", times: 1, status: http.StatusBadGateway, after: true})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
}

// The publish answered 502 after it published. The next pass finds a published
// release with everything in place, and the run is green.
func TestPublishSurvivesAPublishThatFailedButHappened(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "update", times: 1, status: http.StatusBadGateway, after: true})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
}

// A connection that drops is the same uncertainty as a 5xx: whatever was sent may
// or may not have arrived.
func TestPublishRetriesADroppedConnection(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "list", times: 1, drop: true})
	fg.inject(fault{op: "upload", times: 1, drop: true, after: true})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
}

// Rerunning a run that went green, or one whose publish landed after the run was
// marked failed: the release is out and complete, and nothing about it changes.
// Its files stay the bytes people may already have downloaded.
func TestPublishLeavesACompletePublishedReleaseAlone(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	earlier := map[string]string{
		"app-" + tag + "-darwin-amd64.tar.gz": "amd64 archive, earlier build",
		"app-" + tag + "-darwin-arm64.tar.gz": "arm64 archive, earlier build",
		"app-" + tag + "-macos.zip":           "zipped app, earlier build",
		"app-" + tag + "-macos.dmg":           "disk image, earlier build",
	}
	fg.addRelease(tag, false, generatedNotes(tag), earlier)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, earlier)
	if m := fg.mutations(); len(m) != 0 {
		t.Errorf("a complete published release was changed: %v", m)
	}
}

// A published release missing a file gets the file; the ones it has stay.
func TestPublishAddsWhatAPublishedReleaseIsMissing(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	have := map[string]string{
		"app-" + tag + "-darwin-amd64.tar.gz": "amd64 archive, earlier build",
		"app-" + tag + "-darwin-arm64.tar.gz": files["app-"+tag+"-darwin-arm64.tar.gz"],
		"app-" + tag + "-macos.zip":           files["app-"+tag+"-macos.zip"],
	}
	fg.addRelease(tag, false, generatedNotes(tag), have)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for k, v := range files {
		want[k] = v
	}
	want["app-"+tag+"-darwin-amd64.tar.gz"] = "amd64 archive, earlier build"
	requirePublished(t, fg, want)
}

// The release this looks for may be far down a repository's release list, past the
// first page.
func TestPublishFindsTheReleaseBeyondTheFirstPage(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	draft := fg.addRelease(tag, true, "", nil)
	for i := range 150 {
		fg.addRelease(fmt.Sprintf("v0.0.%d", i), false, "notes", nil)
	}

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	if rel := requirePublished(t, fg, files); rel.ID != draft.ID {
		t.Errorf("published release %d, want the existing draft %d", rel.ID, draft.ID)
	}
}

// An outage that outlasts every retry must end the run red, and say the release is
// not out: a draft nobody looks at is how a release goes missing.
func TestPublishFailsWhenThePublishNeverGoesThrough(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.inject(fault{op: "update", times: -1, status: http.StatusBadGateway})
	var slept int
	p := fg.publisher(t.Output(), 4)
	p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

	err := p.Publish(context.Background(), tag, paths)
	if err == nil {
		t.Fatal("publish reported success while the release stayed a draft")
	}
	if !strings.Contains(err.Error(), "not published") {
		t.Errorf("the error does not say the release is not published: %v", err)
	}
	if slept != 4 {
		t.Errorf("waited %d times, want every one of the 4 retries used", slept)
	}
}

// An answer is not the state. If GitHub says a release is published and it is not,
// the read-back afterwards has to catch it.
func TestPublishFailsWhenGitHubClaimsToHavePublishedAndDidNot(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.ignorePublish = true

	err := fg.publisher(t.Output(), 2).Publish(context.Background(), tag, paths)
	if err == nil {
		t.Fatal("publish reported success for a release that is still a draft")
	}
	if rels := fg.releasesForTag(); len(rels) != 1 || !rels[0].Draft {
		t.Fatalf("the fake did not keep the release a draft; the test proves nothing")
	}
}

// A 4xx is GitHub refusing the request, and asking again gets the same answer
// minutes later. The run fails at once, with GitHub's words.
func TestPublishDoesNotRetryARefusal(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.inject(fault{op: "create", times: -1, status: http.StatusForbidden})
	var slept int
	p := fg.publisher(t.Output(), 5)
	p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

	err := p.Publish(context.Background(), tag, paths)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("got %v, want the 403 itself", err)
	}
	if slept != 0 {
		t.Errorf("a refusal was retried %d times", slept)
	}
}

// A 422 on a first attempt is a real validation error. Only after a failure that
// may have written something does it mean "a previous attempt got there first".
func TestPublishDoesNotRetryAValidationErrorWithNothingUncertainBeforeIt(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.inject(fault{op: "create", times: -1, status: http.StatusUnprocessableEntity})
	var slept int
	p := fg.publisher(t.Output(), 5)
	p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

	if err := p.Publish(context.Background(), tag, paths); err == nil {
		t.Fatal("a 422 on the first create was reported as success")
	}
	if slept != 0 {
		t.Errorf("a validation error was retried %d times", slept)
	}
}

// Bad credentials are not an outage.
func TestPublishDoesNotRetryBadCredentials(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	p := fg.publisher(t.Output(), 5)
	p.Token = "wrong"

	var apiErr *APIError
	if err := p.Publish(context.Background(), tag, paths); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("got %v, want the 401", err)
	}
}

// A missing file means the build did not produce what the release needs. Nothing
// is created on GitHub for a release that cannot be completed.
func TestPublishTouchesNothingWhenAnArtifactIsMissing(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	paths = append(paths, filepath.Join(t.TempDir(), "dist", "*.dmg"))

	if err := fg.publisher(t.Output(), 5).Publish(context.Background(), tag, paths); err == nil {
		t.Fatal("publish succeeded with an artifact missing")
	}
	if m := fg.mutations(); len(m) != 0 {
		t.Errorf("GitHub was changed for a release that could not be completed: %v", m)
	}
}

// Two artifacts with one name would overwrite each other on the release.
func TestPublishRefusesTwoArtifactsWithOneName(t *testing.T) {
	fg := newFakeGitHub(t)
	a := artifacts(t, map[string]string{"same.zip": "one"})
	b := artifacts(t, map[string]string{"same.zip": "two"})

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, append(a, b...)); err == nil {
		t.Fatal("publish accepted two artifacts named same.zip")
	}
	if m := fg.mutations(); len(m) != 0 {
		t.Errorf("GitHub was changed: %v", m)
	}
}

// A published release already on the tag and a draft beside it: the published one is
// the release, and the draft is only reported.
func TestPublishPrefersThePublishedReleaseOverADraftOnTheSameTag(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.addRelease(tag, true, "", nil)
	published := fg.addRelease(tag, false, generatedNotes(tag), files)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	if rel := requirePublished(t, fg, files); rel.ID != published.ID {
		t.Errorf("published release is %d, want %d", rel.ID, published.ID)
	}
}
