//go:build e2e

// The same publisher against the real GitHub API, in a repository kept for it.
// The fake server in fakegithub_test.go holds what GitHub is documented or was
// observed to do; these tests are what checks that it does. They create tags and
// releases, so they never run as part of `make test` and never against this
// repository:
//
//	FLEETDECK_E2E_REPO=owner/sandbox GITHUB_TOKEN="$(gh auth token)" \
//	  go test -tags e2e -run TestE2E -count=1 -v ./internal/releasepublish/
//
// The repository needs one commit on its default branch. Every test makes a tag of
// its own, and leaves its releases behind for a person to look at.
//
// GitHub cannot be asked to fail on cue, so the failures are made here: a transport
// between the publisher and GitHub answers chosen requests with an empty 500, either
// without sending them or after GitHub has acted on them. The second one is what
// GitHub itself did on 2026-09-13.

package releasepublish

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type e2eRule struct {
	name  string
	match func(*http.Request) bool
	// times is how often the rule fires; negative is forever.
	times int
	// after sends the request to GitHub before answering 500.
	after bool
}

// faultyTransport answers requests its rules match with a 500 carrying no body, the
// answer GitHub gave on 2026-09-13.
type faultyTransport struct {
	t    *testing.T
	base http.RoundTripper

	mu        sync.Mutex
	rules     []*e2eRule
	mutations []string
}

func (ft *faultyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ft.mu.Lock()
	if req.Method != http.MethodGet {
		ft.mutations = append(ft.mutations, req.Method+" "+req.URL.Path)
	}
	var fire *e2eRule
	for _, r := range ft.rules {
		if r.times != 0 && r.match(req) {
			if r.times > 0 {
				r.times--
			}
			fire = r
			break
		}
	}
	ft.mu.Unlock()

	if fire == nil {
		return ft.base.RoundTrip(req)
	}
	if fire.after {
		resp, err := ft.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		ft.t.Logf("fault %q: %s %s reached GitHub (HTTP %d), answering 500", fire.name, req.Method, req.URL.Path, resp.StatusCode)
	} else {
		if req.Body != nil {
			req.Body.Close()
		}
		ft.t.Logf("fault %q: %s %s not sent, answering 500", fire.name, req.Method, req.URL.Path)
	}
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Status:     "500 Internal Server Error",
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:        http.Header{},
		Body:          io.NopCloser(bytes.NewReader(nil)),
		ContentLength: 0,
		Request:       req,
	}, nil
}

func (ft *faultyTransport) add(r e2eRule) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.rules = append(ft.rules, &r)
}

func (ft *faultyTransport) takeMutations() []string {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	out := ft.mutations
	ft.mutations = nil
	return out
}

var (
	releasesPath = regexp.MustCompile(`^/repos/[^/]+/[^/]+/releases$`)
	releasePath  = regexp.MustCompile(`^/repos/[^/]+/[^/]+/releases/\d+$`)
	uploadPath   = regexp.MustCompile(`^/repos/[^/]+/[^/]+/releases/\d+/assets$`)
)

func isCreate(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Host == "api.github.com" && releasesPath.MatchString(r.URL.Path)
}

func isUpdate(r *http.Request) bool {
	return r.Method == http.MethodPatch && releasePath.MatchString(r.URL.Path)
}

func isUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Host == "uploads.github.com" && uploadPath.MatchString(r.URL.Path)
}

type e2e struct {
	t    *testing.T
	repo string
	tag  string
	ft   *faultyTransport
	// admin talks to GitHub without faults, to set a scene and to look at it.
	admin *Publisher
	files map[string]string
	paths []string
}

func newE2E(t *testing.T, scenario string) *e2e {
	t.Helper()
	repo, token := os.Getenv("FLEETDECK_E2E_REPO"), os.Getenv("GITHUB_TOKEN")
	if repo == "" || token == "" {
		t.Skip("FLEETDECK_E2E_REPO and GITHUB_TOKEN are required")
	}
	if repo == "kroticw/fleetdeck" {
		t.Fatal("the end-to-end tests make releases; they do not run against the project's own repository")
	}
	e := &e2e{
		t:     t,
		repo:  repo,
		tag:   fmt.Sprintf("v0.0.%d-e2e-%s", time.Now().Unix(), scenario),
		ft:    &faultyTransport{t: t, base: http.DefaultTransport},
		admin: &Publisher{API: "https://api.github.com", Repo: repo, Token: token, Client: &http.Client{}},
	}
	e.makeTag()
	e.makeBuild()
	return e
}

// publisher is the program as the workflow runs it, with the fault transport between
// it and GitHub and short waits.
func (e *e2e) publisher(waits ...time.Duration) *Publisher {
	return &Publisher{
		API:    "https://api.github.com",
		Repo:   e.repo,
		Token:  e.admin.Token,
		Client: &http.Client{Transport: e.ft},
		Log:    e.t.Output(),
		Waits:  waits,
	}
}

func (e *e2e) makeTag() {
	ctx := context.Background()
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	e.must(e.admin.call(ctx, http.MethodGet, e.admin.repoURL(""), nil, &repo))
	var head struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	e.must(e.admin.call(ctx, http.MethodGet, e.admin.repoURL("/git/ref/heads/"+repo.DefaultBranch), nil, &head))
	e.must(e.admin.call(ctx, http.MethodPost, e.admin.repoURL("/git/refs"), map[string]any{
		"ref": "refs/tags/" + e.tag,
		"sha": head.Object.SHA,
	}, nil))
	e.t.Logf("tag %s on %s", e.tag, head.Object.SHA)
}

// makeBuild writes four small artifacts named the way a release names them, with
// content no earlier run had.
func (e *e2e) makeBuild() {
	dir := e.t.TempDir()
	e.files = map[string]string{}
	for _, suffix := range []string{"darwin-amd64.tar.gz", "darwin-arm64.tar.gz", "macos.zip", "macos.dmg"} {
		name := "fleetdeck-" + e.tag + "-" + suffix
		content := name + " " + randomHex(e.t)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			e.t.Fatal(err)
		}
		e.files[name] = content
		e.paths = append(e.paths, p)
	}
}

func (e *e2e) releases() []release {
	all, err := e.admin.listReleases(context.Background())
	e.must(err)
	var out []release
	for _, r := range all {
		if r.TagName == e.tag {
			out = append(out, r)
		}
	}
	return out
}

// requirePublished is the acceptance, read from GitHub with nothing in between: the
// release for the tag is published, has generated notes, and holds exactly this
// build's files, each uploaded with the digest of the local file.
func (e *e2e) requirePublished() release {
	e.t.Helper()
	var published []release
	for _, r := range e.releases() {
		if !r.Draft {
			published = append(published, r)
		}
	}
	if len(published) != 1 {
		e.t.Fatalf("%d published releases carry %s, want one", len(published), e.tag)
	}
	var rel release
	e.must(e.admin.call(context.Background(), http.MethodGet, e.admin.repoURL(fmt.Sprintf("/releases/%d", published[0].ID)), nil, &rel))
	if rel.Body == "" || !strings.Contains(rel.Body, "Full Changelog") {
		e.t.Errorf("the release notes were not generated: %q", rel.Body)
	}
	arts, err := readArtifacts(e.paths)
	e.must(err)
	for _, a := range arts {
		have := findAsset(rel.Assets, a.name)
		switch {
		case have == nil:
			e.t.Errorf("%s is missing", a.name)
		case have.State != stateUploaded:
			e.t.Errorf("%s is in state %q", a.name, have.State)
		case !have.holds(a):
			e.t.Errorf("%s on GitHub is %d bytes, digest %v; the local file is %d bytes, %s", a.name, have.Size, deref(have.Digest), a.size, a.digest)
		}
	}
	if len(rel.Assets) != len(arts) {
		e.t.Errorf("the release holds %d assets, want %d", len(rel.Assets), len(arts))
	}
	e.t.Logf("published: %s", rel.HTMLURL)
	return rel
}

func (e *e2e) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

func randomHex(t *testing.T) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

var e2eWaits = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}

// The premise, checked rather than assumed: a draft is not found by its tag, so a
// lookup by tag reports no release while one is in the way.
func TestE2EADraftIsNotFoundByItsTag(t *testing.T) {
	e := newE2E(t, "lookup")
	var draft release
	e.must(e.admin.call(context.Background(), http.MethodPost, e.admin.repoURL("/releases"), map[string]any{"tag_name": e.tag, "draft": true}, &draft))

	err := e.admin.call(context.Background(), http.MethodGet, e.admin.repoURL("/releases/tags/"+e.tag), nil, nil)
	if apiErr, ok := err.(*APIError); !ok || apiErr.Status != http.StatusNotFound {
		t.Fatalf("looking draft %d up by its tag answered %v, want 404", draft.ID, err)
	}
	if n := len(e.releases()); n != 1 {
		t.Fatalf("the release list shows %d releases on the tag, want the draft", n)
	}
}

// 2026-09-13, as it happened: the create answered 500 and made the release.
func TestE2ECreateThatFailedButHappened(t *testing.T) {
	e := newE2E(t, "create500")
	e.ft.add(e2eRule{name: "create 500 after creating", match: isCreate, times: 1, after: true})

	if err := e.publisher(e2eWaits...).Publish(context.Background(), e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	e.requirePublished()
	if n := len(e.releases()); n != 1 {
		t.Errorf("%d releases carry the tag, want one", n)
	}
}

// An empty draft on the tag, the kind a create that reported failure leaves: the
// publish finishes it rather than adding another.
func TestE2EFinishesALeftoverEmptyDraft(t *testing.T) {
	e := newE2E(t, "draft")
	ctx := context.Background()
	var draft release
	e.must(e.admin.call(ctx, http.MethodPost, e.admin.repoURL("/releases"), map[string]any{"tag_name": e.tag, "draft": true}, &draft))

	if err := e.publisher().Publish(ctx, e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	if rel := e.requirePublished(); rel.ID != draft.ID {
		t.Errorf("published %d, want the draft that was there, %d", rel.ID, draft.ID)
	}
	if n := len(e.releases()); n != 1 {
		t.Errorf("%d releases carry the tag, want one", n)
	}
}

// The state v0.8.0 was left in: two drafts on the tag. Nothing says which is the
// release, so the run stops and changes nothing.
func TestE2EStopsOnTwoDrafts(t *testing.T) {
	e := newE2E(t, "drafts")
	ctx := context.Background()
	var first, second release
	e.must(e.admin.call(ctx, http.MethodPost, e.admin.repoURL("/releases"), map[string]any{"tag_name": e.tag, "draft": true}, &first))
	e.must(e.admin.call(ctx, http.MethodPost, e.admin.repoURL("/releases"), map[string]any{"tag_name": e.tag, "draft": true}, &second))
	t.Logf("GitHub accepted two drafts on one tag: %d and %d", first.ID, second.ID)

	err := e.publisher(e2eWaits...).Publish(ctx, e.tag, e.paths)
	if err == nil {
		t.Fatal("the publish chose one of two drafts on its own")
	}
	t.Logf("stopped with: %v", err)
	if m := e.ft.takeMutations(); len(m) != 0 {
		t.Errorf("GitHub was changed: %v", m)
	}
}

// A draft made by an earlier attempt of this publisher carries GitHub's generated
// notes. Finishing it depends on GitHub generating the same notes again when asked,
// which is what this checks.
func TestE2EFinishesADraftAnEarlierAttemptMade(t *testing.T) {
	e := newE2E(t, "earlier")
	e.ft.add(e2eRule{name: "uploads down", match: isUpload, times: -1})
	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err == nil {
		t.Fatal("a run with uploads failing reported success")
	}
	rels := e.releases()
	if len(rels) != 1 || !rels[0].Draft || rels[0].Body == "" {
		t.Fatalf("want one draft carrying generated notes: %+v", rels)
	}

	e.ft = &faultyTransport{t: t, base: http.DefaultTransport}
	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	e.requirePublished()
}

// A run that died part way through the uploads: one file on a draft, nothing else.
// The rerun finishes it.
func TestE2ERerunFinishesARunThatDiedDuringUploads(t *testing.T) {
	e := newE2E(t, "died")
	uploads := 0
	e.ft.add(e2eRule{name: "run dies after the first upload", times: -1, match: func(r *http.Request) bool {
		if isUpload(r) {
			uploads++
			return uploads > 1
		}
		return uploads > 1
	}})

	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err == nil {
		t.Fatal("the interrupted run reported success")
	}
	rels := e.releases()
	if len(rels) != 1 || !rels[0].Draft || len(rels[0].Assets) != 1 {
		t.Fatalf("the interrupted run did not leave one draft with one file: %+v", rels)
	}

	e.ft = &faultyTransport{t: t, base: http.DefaultTransport}
	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	if rel := e.requirePublished(); rel.ID != rels[0].ID {
		t.Errorf("published %d, want the draft the first run left, %d", rel.ID, rels[0].ID)
	}
}

// A rerun after a build that differs: the draft holds the earlier build's files, and
// they are replaced.
func TestE2ERerunReplacesAnEarlierBuildOnADraft(t *testing.T) {
	e := newE2E(t, "replace")
	e.ft.add(e2eRule{name: "publish refused", match: isUpdate, times: -1})
	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err == nil {
		t.Fatal("a run whose publish failed reported success")
	}
	if rels := e.releases(); len(rels) != 1 || !rels[0].Draft || len(rels[0].Assets) != 4 {
		t.Fatalf("want one draft with all four files of the first build: %+v", rels)
	}

	e.makeBuild()
	e.ft = &faultyTransport{t: t, base: http.DefaultTransport}
	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	e.requirePublished()
}

// An upload that answered 500 after GitHub stored the file, and a publish that
// answered 500 after GitHub published.
func TestE2EUploadAndPublishThatFailedButHappened(t *testing.T) {
	e := newE2E(t, "write500")
	e.ft.add(e2eRule{name: "upload 500 after storing", match: isUpload, times: 1, after: true})
	e.ft.add(e2eRule{name: "publish 500 after publishing", match: isUpdate, times: 1, after: true})

	if err := e.publisher(e2eWaits...).Publish(context.Background(), e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	e.requirePublished()
}

// An outage of publishing longer than the retries: the run fails, says so, and
// leaves a draft holding every file. The rerun of the publish, with the same files,
// only publishes; a rerun after that changes nothing at all.
func TestE2EOutageLeavesADraftTheRerunOnlyPublishes(t *testing.T) {
	e := newE2E(t, "outage")
	e.ft.add(e2eRule{name: "publishing down", match: isUpdate, times: -1})

	err := e.publisher(time.Second, time.Second).Publish(context.Background(), e.tag, e.paths)
	if err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("an outage outlasting the retries ended with %v, want a failure saying the release is not published", err)
	}
	rels := e.releases()
	if len(rels) != 1 || !rels[0].Draft || len(rels[0].Assets) != len(e.paths) {
		t.Fatalf("the failed run did not leave one draft with every file: %+v", rels)
	}

	e.ft = &faultyTransport{t: t, base: http.DefaultTransport}
	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	e.requirePublished()
	if m := e.ft.takeMutations(); len(m) != 1 || !strings.HasPrefix(m[0], "PATCH ") {
		t.Errorf("the rerun sent %v, want the publish alone", m)
	}

	if err := e.publisher().Publish(context.Background(), e.tag, e.paths); err != nil {
		t.Fatal(err)
	}
	if m := e.ft.takeMutations(); len(m) != 0 {
		t.Errorf("rerunning a finished release changed it: %v", m)
	}
}
