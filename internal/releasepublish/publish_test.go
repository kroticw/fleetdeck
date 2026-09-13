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

// A create that answered with an error and made an empty draft anyway: the retry
// finishes that draft rather than adding a second one beside it.
func TestPublishFinishesALeftoverEmptyDraftInsteadOfCreatingAnother(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	draft := fg.addRelease(tag, true, "", nil)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	if rel := requirePublished(t, fg, files); rel.ID != draft.ID {
		t.Errorf("published release %d, want the existing draft %d", rel.ID, draft.ID)
	}
	if n := len(fg.releasesForTag()); n != 1 {
		t.Errorf("%d releases carry the tag, want the one that was there", n)
	}
}

// v0.8.0 was left with two drafts on the tag. Nothing says which one is the release,
// and publishing the wrong one cannot be taken back: the run stops, names them, and
// changes nothing.
func TestPublishStopsOnSeveralDraftsOnTheTag(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	first := fg.addRelease(tag, true, "", nil)
	second := fg.addRelease(tag, true, "", nil)
	var slept int
	p := fg.publisher(t.Output(), 5)
	p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

	err := p.Publish(context.Background(), tag, paths)
	if err == nil {
		t.Fatal("publish chose one of two drafts on its own")
	}
	for _, id := range []int64{first.ID, second.ID} {
		if !strings.Contains(err.Error(), fmt.Sprint(id)) {
			t.Errorf("the error does not name draft %d: %v", id, err)
		}
	}
	if m := fg.mutations(); len(m) != 0 {
		t.Errorf("GitHub was changed: %v", m)
	}
	if slept != 0 {
		t.Errorf("waited %d times for something only a person can resolve", slept)
	}
}

// A draft somebody shaped would be published with whatever they put in it. Each of
// these stops the run before anything changes.
func TestPublishStopsOnADraftCarryingSomethingAPublishWouldNotPutThere(t *testing.T) {
	cases := map[string]func(*fakeRelease){
		"prerelease":         func(r *fakeRelease) { r.Prerelease = true },
		"hand-written notes": func(r *fakeRelease) { r.Name, r.Body = tag, "Hand-written notes." },
		"another title":      func(r *fakeRelease) { r.Name, r.Body = "Big release", generatedNotes(tag) },
		"a file of its own": func(r *fakeRelease) {
			r.Assets = append(r.Assets, &fakeAsset{ID: 1, Name: "checksums.txt", State: "uploaded", Data: []byte("x")})
		},
	}
	for name, shape := range cases {
		t.Run(name, func(t *testing.T) {
			fg := newFakeGitHub(t)
			_, paths := build(t)
			shape(fg.addRelease(tag, true, "", nil))

			err := fg.publisher(t.Output(), 5).Publish(context.Background(), tag, paths)
			if err == nil {
				t.Fatal("publish finished a draft carrying something it did not make")
			}
			if m := fg.mutations(); len(m) != 0 {
				t.Errorf("GitHub was changed: %v", m)
			}
		})
	}
}

// The price of an outage that outlasts every retry has to stay one rerun of the
// publish, not a new build: the draft keeps every file that reached it, and a rerun
// with the same files only publishes.
func TestPublishOutageLeavesADraftARerunOnlyHasToPublish(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "update", times: -1, status: http.StatusBadGateway})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err == nil {
		t.Fatal("publish reported success while publishing was failing")
	}
	rels := fg.releasesForTag()
	if len(rels) != 1 || !rels[0].Draft || len(rels[0].Assets) != len(files) {
		t.Fatalf("the failed run did not leave one draft with every file on it")
	}

	fg.recover()
	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	want := fmt.Sprintf("PATCH /repos/%s/releases/%d", fakeRepo, rels[0].ID)
	if m := fg.mutations(); len(m) != 1 || m[0] != want {
		t.Errorf("the rerun sent %v, want only %s", m, want)
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
	var log bytes.Buffer

	if err := fg.publisher(&log, 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, earlier)
	if m := fg.mutations(); len(m) != 0 {
		t.Errorf("a complete published release was changed: %v", m)
	}
	// Green is right, and so is saying what it means: the release people download is
	// not this build.
	warning := ""
	for _, line := range strings.Split(log.String(), "\n") {
		if strings.HasPrefix(line, "::warning::") {
			warning = line
		}
	}
	for name := range earlier {
		if !strings.Contains(warning, name) {
			t.Errorf("no warning names %s, whose bytes are not this build's; log:\n%s", name, log.String())
		}
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
	if n := len(fg.publishRequests); n != 1 {
		t.Errorf("%d publish requests were sent, want one; the retries only read", n)
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
	var slept int
	p := fg.publisher(t.Output(), 5)
	p.Token = "wrong"
	p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

	var apiErr *APIError
	if err := p.Publish(context.Background(), tag, paths); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("got %v, want the 401", err)
	}
	if slept != 0 {
		t.Errorf("bad credentials were retried %d times", slept)
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

// The list trails a create. A create that answered with an error and went through is
// not in the list on the next reading; creating again then would make a second draft.
// The publisher reads the list again, with growing pauses, before it does.
func TestPublishLooksAgainBeforeCreatingWhenACreateMayHaveGoneThrough(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.listLag = 3
	fg.inject(fault{op: "create", times: 1, status: http.StatusInternalServerError, after: true})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	if n := len(fg.releasesForTag()); n != 1 {
		t.Errorf("%d releases carry the tag, want one", n)
	}
}

// A release this run created and got an answer for is read by its id while the list
// does not show it yet, instead of being made again.
func TestPublishReadsTheReleaseItCreatedByIDWhileTheListTrails(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.listLag = 3
	fg.inject(fault{op: "upload", times: 1, status: http.StatusBadGateway})
	p := fg.publisher(t.Output(), 3)
	p.Settle = nil

	if err := p.Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	if n := len(fg.releasesForTag()); n != 1 {
		t.Errorf("%d releases carry the tag, want one", n)
	}
}

// Before publishing, the draft is read by its id. If GitHub reports another tag on
// it, it is not published.
func TestPublishDoesNotPublishADraftThatReadsBackWithAnotherTag(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.mangle = "read"
	var slept int
	p := fg.publisher(t.Output(), 5)
	p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

	if err := p.Publish(context.Background(), tag, paths); err == nil {
		t.Fatal("publish succeeded with a draft reading back as untagged")
	}
	if n := len(fg.publishRequests); n != 0 {
		t.Errorf("%d publish requests were sent", n)
	}
	if slept != 0 {
		t.Errorf("retried %d times", slept)
	}
}

// 2026-09-13 in the sandbox: a release came back published under untagged-..., and
// the retries went on to publish two more. Once a publish has gone out and the release
// is wrong, the run stops and sends nothing else.
func TestPublishStopsWhenAPublishedReleaseComesBackWithAnotherTag(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.mangle = "publish"
	fg.addRelease(tag, true, "", nil)

	err := fg.publisher(t.Output(), 5).Publish(context.Background(), tag, paths)
	if err == nil {
		t.Fatal("publish succeeded with the release published under another tag")
	}
	if n := len(fg.publishRequests); n != 1 {
		t.Errorf("%d publish requests were sent, want the one", n)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(fg.publishRequests[0].ID)) {
		t.Errorf("the error does not name the release: %v", err)
	}
	creates := 0
	for _, m := range fg.mutations() {
		if m == "POST /repos/"+fakeRepo+"/releases" {
			creates++
		}
	}
	if creates != 0 {
		t.Errorf("%d releases were created after the publish", creates)
	}
}

// A publish request is sent once. When it fails, the run only reads the release
// afterwards, even though nothing was published: a read can show a published release
// as a draft, and acting on that read could write to something public. The run ends
// red, and a rerun of the publish, which is a minute, finishes the release.
func TestPublishSendsItsPublishRequestOnce(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "publish", times: 1, status: http.StatusInternalServerError})

	err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths)
	if err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("a publish request that failed ended with %v, want a failure saying the release is not published", err)
	}
	if n := len(fg.publishRequests); n != 1 {
		t.Errorf("%d publish requests were sent, want one", n)
	}
	for _, w := range fg.writes {
		if w.AfterPublish {
			t.Errorf("a %s went to release %d after the publish request", w.Op, w.ID)
		}
	}

	fg.recover()
	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
}

// GitHub may give no digest for an asset. A matching size says nothing about the
// bytes, so the file is downloaded and hashed: the same bytes are not uploaded again,
// and the release is still published.
func TestPublishChecksTheBytesOfFilesGitHubGivesNoDigestFor(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.mangle = "no-digest"
	fg.addRelease(tag, true, generatedNotes(tag), files)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	if n := fg.uploads(); n != 0 {
		t.Errorf("%d files with this build's bytes were uploaded again", n)
	}
	if fg.downloads() == 0 {
		t.Error("no file was downloaded, so no bytes were checked")
	}
}

// With no digest to go by, bytes GitHub stored differently are found by hashing them,
// and the draft is not published.
func TestPublishDoesNotPublishBytesThatDifferWhenGitHubGivesNoDigest(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.mangle = "no-digest-corrupt"

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err == nil {
		t.Fatal("publish succeeded with every file stored wrong")
	}
	if n := len(fg.publishRequests); n != 0 {
		t.Errorf("%d publish requests were sent for files whose bytes differ", n)
	}
}

// A 422 after a failed read is a refusal: a read changes nothing, so nothing an
// earlier attempt did can explain it.
func TestPublishDoesNotRetryAValidationErrorAfterAFailedRead(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.inject(fault{op: "list", times: 1, status: http.StatusBadGateway})
	fg.inject(fault{op: "create", times: -1, status: http.StatusUnprocessableEntity})
	var slept int
	p := fg.publisher(t.Output(), 5)
	p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

	if err := p.Publish(context.Background(), tag, paths); err == nil {
		t.Fatal("a refused create was reported as success")
	}
	if slept != 1 {
		t.Errorf("waited %d times, want the one for the failed read", slept)
	}
}

// A publish has a limit of its own, well under the job's, and says it ran out of time
// rather than being cancelled with the job.
func TestPublishGivesUpAtItsDeadline(t *testing.T) {
	fg := newFakeGitHub(t)
	_, paths := build(t)
	fg.inject(fault{op: "list", times: -1, status: http.StatusBadGateway})
	p := fg.publisher(t.Output(), 3)
	p.Waits = []time.Duration{time.Hour, time.Hour, time.Hour}
	p.Sleep = nil
	p.Deadline = 200 * time.Millisecond

	done := make(chan error, 1)
	go func() { done <- p.Publish(context.Background(), tag, paths) }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "within") {
			t.Fatalf("got %v, want a failure saying the publish ran out of its time", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("publish was still waiting 10 seconds after its 200ms deadline")
	}
}

// The property the sandbox findings come down to, checked across every way GitHub
// was seen to answer wrongly, alone and together: a publish request only ever goes to
// a draft carrying our tag, one run publishes at most one release, and a run that
// succeeds leaves exactly one complete published release with our tag.
func TestPublishNeverPublishesAReleaseWhoseTagIsNotOurs(t *testing.T) {
	manglings := []string{"", "create", "notes", "unnamed-update", "read", "publish", "drop", "no-digest", "no-digest-corrupt"}
	faults := map[string][]fault{
		"no failure":                    nil,
		"create 500 after creating":     {{op: "create", times: 1, status: http.StatusInternalServerError, after: true}},
		"upload 502 after storing":      {{op: "upload", times: 1, status: http.StatusBadGateway, after: true}},
		"upload answered, not stored":   {{op: "upload", times: 1, lie: true}},
		"update 500 after applying":     {{op: "update", times: 1, status: http.StatusInternalServerError, after: true}},
		"publish 500 after publishing":  {{op: "publish", times: 1, status: http.StatusInternalServerError, after: true}},
		"publish 502 before publishing": {{op: "publish", times: 2, status: http.StatusBadGateway}},
		"read dropped":                  {{op: "get", times: 1, drop: true}},
	}
	starts := map[string]func(*fakeGitHub){
		"nothing":        func(*fakeGitHub) {},
		"an empty draft": func(fg *fakeGitHub) { fg.addRelease(tag, true, "", nil) },
		"two drafts, one not listed yet": func(fg *fakeGitHub) {
			fg.addRelease(tag, true, "", nil)
			fg.hide(fg.addRelease(tag, true, "", nil), 2)
		},
	}
	for _, mangle := range manglings {
		for faultName, injected := range faults {
			for startName, start := range starts {
				for _, lag := range []int{0, 2} {
					name := fmt.Sprintf("mangle=%q/%s/%s/lag=%d", mangle, faultName, startName, lag)
					t.Run(name, func(t *testing.T) {
						fg := newFakeGitHub(t)
						files, paths := build(t)
						fg.mangle, fg.listLag = mangle, lag
						start(fg)
						for _, f := range injected {
							fg.inject(f)
						}

						err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths)

						for _, r := range fg.publishRequests {
							if r.Tag != tag || !r.Draft {
								t.Errorf("a publish request went to release %d while it carried %q (draft %v)", r.ID, r.Tag, r.Draft)
							}
							held := map[string]string{}
							for _, a := range r.Assets {
								if a.State == "uploaded" {
									held[a.Name] = string(a.Data)
								}
							}
							for name, content := range files {
								if held[name] != content {
									t.Errorf("a publish request went to release %d while it did not hold this build's %s", r.ID, name)
								}
							}
						}
						for _, w := range fg.writes {
							if w.Tag != tag {
								t.Errorf("a %s went to release %d while it carried %q", w.Op, w.ID, w.Tag)
							}
							if !w.Draft {
								t.Errorf("a %s went to release %d after it was published", w.Op, w.ID)
							}
							if w.AfterPublish {
								t.Errorf("a %s went to release %d after a publish request had been sent", w.Op, w.ID)
							}
						}
						if n := len(fg.publishRequests); n > 1 {
							t.Errorf("%d publish requests were sent in one run", n)
						}
						if n := len(fg.published); n > 1 {
							t.Errorf("%d releases were published in one run: %v", n, fg.published)
						}
						if err == nil {
							requirePublished(t, fg, files)
							for _, r := range fg.publishedReleases() {
								if r.Tag != tag {
									t.Errorf("the run succeeded and release %d is published as %q", r.ID, r.Tag)
								}
							}
						}
					})
				}
			}
		}
	}
}

// The sandbox, 2026-09-13: writing notes to an empty draft without naming its tag
// came back with the draft's tag replaced by untagged-.... Naming the tag in that
// update keeps the draft publishable.
func TestPublishNamesTheTagWhenWritingNotesToADraft(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.mangle = "unnamed-update"
	draft := fg.addRelease(tag, true, "", nil)

	if err := fg.publisher(t.Output(), 0).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	if rel := requirePublished(t, fg, files); rel.ID != draft.ID {
		t.Errorf("published release %d, want the draft %d", rel.ID, draft.ID)
	}
}

// A release found published and missing a file gets it once. When that upload fails,
// the next attempt only reads the release: writing to something public again is a
// retry after publication.
func TestPublishWritesToAPublishedReleaseOnlyOnce(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	have := map[string]string{}
	for name, content := range files {
		if !strings.HasSuffix(name, ".dmg") {
			have[name] = content
		}
	}
	fg.addRelease(tag, false, generatedNotes(tag), have)
	fg.inject(fault{op: "upload", times: -1, status: http.StatusBadGateway})

	if err := fg.publisher(t.Output(), 5).Publish(context.Background(), tag, paths); err == nil {
		t.Fatal("publish succeeded with the image never uploaded")
	}
	if n := fg.uploads(); n != 1 {
		t.Errorf("%d uploads went to the published release, want one", n)
	}
}

// A published release that drops out of the list under its tag, the way a release
// given another tag does, is still the release: the next attempt reads it by its id
// rather than making a new one.
func TestPublishStaysWithAPublishedReleaseThatLeavesTheList(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	have := map[string]string{}
	for name, content := range files {
		if !strings.HasSuffix(name, ".dmg") {
			have[name] = content
		}
	}
	fg.vanish(fg.addRelease(tag, false, generatedNotes(tag), have), 1)
	fg.inject(fault{op: "upload", times: 1, status: http.StatusBadGateway})

	_ = fg.publisher(t.Output(), 5).Publish(context.Background(), tag, paths)
	for _, m := range fg.mutations() {
		if m == "POST /repos/"+fakeRepo+"/releases" {
			t.Errorf("a release was created after a published one had been found")
		}
	}
}

// A release that loses a file as it is published is published and wrong. The run
// stops there and uploads nothing into it: that is a retry after publication, even
// when the publish request's own answer was an error.
func TestPublishStopsWhenTheReleaseComesOutOfPublishingIncomplete(t *testing.T) {
	for name, injected := range map[string][]fault{
		"publish answered":             nil,
		"publish 500 after publishing": {{op: "publish", times: 1, status: http.StatusInternalServerError, after: true}},
	} {
		t.Run(name, func(t *testing.T) {
			fg := newFakeGitHub(t)
			_, paths := build(t)
			fg.mangle = "drop"
			for _, f := range injected {
				fg.inject(f)
			}
			var slept int
			p := fg.publisher(t.Output(), 5)
			p.Sleep = func(context.Context, time.Duration) error { slept++; return nil }

			if err := p.Publish(context.Background(), tag, paths); err == nil {
				t.Fatal("publish succeeded with a file lost in publishing")
			}
			if n := fg.uploads(); n != len(paths) {
				t.Errorf("%d uploads, want the %d before publishing and none after", n, len(paths))
			}
			if want := len(injected); slept != want {
				t.Errorf("waited %d times, want %d", slept, want)
			}
		})
	}
}

// An upload GitHub answered and did not keep is found missing before the publish, not
// after it: the draft gets the file again and only then is published.
func TestPublishDoesNotPublishADraftMissingAFile(t *testing.T) {
	fg := newFakeGitHub(t)
	files, paths := build(t)
	fg.inject(fault{op: "upload", times: 1, lie: true})

	if err := fg.publisher(t.Output(), 3).Publish(context.Background(), tag, paths); err != nil {
		t.Fatal(err)
	}
	requirePublished(t, fg, files)
	if n := len(fg.publishRequests); n != 1 || len(fg.publishRequests[0].Assets) != len(files) {
		t.Errorf("publish requests: %d, the first to a release holding %d files; want one, to a release holding %d", n, len(fg.publishRequests[0].Assets), len(files))
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
