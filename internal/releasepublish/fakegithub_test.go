package releasepublish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is the part of the GitHub REST API a publish touches, kept in memory,
// with the failures GitHub produced on 2026-09-13 available on demand: an error
// returned without doing anything, an error returned after doing it anyway, an
// upload left behind half made, and a connection dropped mid-request.
//
// It encodes what GitHub is documented or was observed to do, not what this package
// assumes: drafts are listed but never found by tag, several drafts may share a tag,
// a second published release for a tag is refused, and an asset name already taken
// on a release is refused.
type fakeGitHub struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	nextID   int64
	releases []*fakeRelease
	faults   []*fault
	requests []string
	// ignorePublish makes an update asking to publish answer as if it had, and
	// publish nothing: the lie a read-back exists to catch.
	ignorePublish bool
	// listLag is how many readings of the release list a new release stays out of:
	// the list trailed a create by one to two seconds on 2026-09-13.
	listLag int
	// mangle is where GitHub gives a release a tag named untagged-... in place of
	// its own: "create", "notes" (an update that does not publish), "publish", or
	// "read" (a release read by its id reports it, while its state keeps the tag).
	// "drop" loses a file of the release as it is published. "unnamed-update" is
	// what the sandbox showed on 2026-09-13: an update of a draft that does not
	// publish it and does not name its tag leaves it with untagged-....
	mangle string

	// publishRequests are the requests to publish that reached the server, with the
	// release as it was when each arrived.
	publishRequests []publishRequest
	// writes are all requests that change a release (uploads, updates, asset
	// deletions), with the release as it was when each arrived.
	writes []writeRequest
	// published are the releases that went from draft to published.
	published []int64
}

type publishRequest struct {
	ID     int64
	Tag    string
	Draft  bool
	Assets []fakeAsset
}

type writeRequest struct {
	Op    string
	ID    int64
	Tag   string
	Draft bool
	// AfterPublish: a publish request had already arrived when this one did.
	AfterPublish bool
}

type fakeRelease struct {
	ID         int64
	Tag        string
	Name       string
	Body       string
	Draft      bool
	Prerelease bool
	Assets     []*fakeAsset
	// unlisted is how many more readings of the list leave this release out.
	unlisted int
	// vanishAfter, when set, is how many readings of the list show this release
	// before it stops appearing in the list for good, the way a release given
	// another tag drops out of the list for its own.
	vanishAfter int
	listed      int
}

func untagged(id int64) string { return fmt.Sprintf("untagged-%016x", id) }

type fakeAsset struct {
	ID          int64
	Name        string
	State       string
	ContentType string
	Data        []byte
}

// fault fires on the next request of op, times times (a negative count fires
// forever).
type fault struct {
	op     string
	times  int
	status int
	// after applies the request before answering with status: the write that
	// reports failure and happened.
	after bool
	// starter leaves an upload behind as an empty asset in state "starter", which
	// is what GitHub documents a 502 during an upload may do.
	starter bool
	// drop closes the connection instead of answering.
	drop bool
	// lie answers as if the request had succeeded, and does nothing.
	lie bool
}

const fakeRepo = "octo/app"

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	fg := &fakeGitHub{t: t, nextID: 1000}
	fg.srv = httptest.NewServer(http.HandlerFunc(fg.serve))
	t.Cleanup(fg.srv.Close)
	return fg
}

func (fg *fakeGitHub) publisher(log io.Writer, waits int) *Publisher {
	return &Publisher{
		API:    fg.srv.URL,
		Repo:   fakeRepo,
		Token:  "test-token",
		Client: fg.srv.Client(),
		Log:    log,
		Waits:  make([]time.Duration, waits),
		Settle: make([]time.Duration, 5),
		Sleep:  func(context.Context, time.Duration) error { return nil },
	}
}

// hide keeps rel out of the next n readings of the release list.
func (fg *fakeGitHub) hide(rel *fakeRelease, n int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	rel.unlisted = n
}

// vanish lets rel appear in n more readings of the release list and in none after.
func (fg *fakeGitHub) vanish(rel *fakeRelease, n int) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	rel.vanishAfter, rel.listed = n, 0
}

// uploads counts the upload requests that reached the server.
func (fg *fakeGitHub) uploads() int {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	n := 0
	for _, w := range fg.writes {
		if w.Op == "upload" {
			n++
		}
	}
	return n
}

// publishedReleases are the releases that are published now, whatever their tag.
func (fg *fakeGitHub) publishedReleases() []fakeRelease {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	var out []fakeRelease
	for _, rel := range fg.releases {
		if !rel.Draft {
			out = append(out, *rel)
		}
	}
	return out
}

func (fg *fakeGitHub) inject(f fault) {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.faults = append(fg.faults, &f)
}

// addRelease puts a release in place before a publish starts: the state an earlier,
// interrupted run or a person left behind.
func (fg *fakeGitHub) addRelease(tag string, draft bool, body string, assets map[string]string) *fakeRelease {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	rel := &fakeRelease{ID: fg.id(), Tag: tag, Draft: draft, Body: body}
	if body != "" {
		rel.Name = tag
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rel.Assets = append(rel.Assets, &fakeAsset{ID: fg.id(), Name: name, State: "uploaded", ContentType: "application/octet-stream", Data: []byte(assets[name])})
	}
	fg.releases = append(fg.releases, rel)
	return rel
}

// releasesForTag are the releases carrying the tag the tests publish.
func (fg *fakeGitHub) releasesForTag() []*fakeRelease {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	var out []*fakeRelease
	for _, rel := range fg.releases {
		if rel.Tag == tag {
			out = append(out, rel)
		}
	}
	return out
}

// mutations are the requests that change something, in the order they arrived.
// Generating notes is a POST that changes nothing, and is not one.
func (fg *fakeGitHub) mutations() []string {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	var out []string
	for _, r := range fg.requests {
		if !strings.HasPrefix(r, "GET ") && !strings.HasSuffix(r, "/generate-notes") {
			out = append(out, r)
		}
	}
	return out
}

// recover ends every injected failure and forgets the requests seen so far: GitHub
// is back, and what a rerun does from here is counted on its own.
func (fg *fakeGitHub) recover() {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	fg.faults = nil
	fg.requests = nil
}

func (fg *fakeGitHub) id() int64 {
	fg.nextID++
	return fg.nextID
}

func (fg *fakeGitHub) takeFault(op string) *fault {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	for _, f := range fg.faults {
		if f.op != op || f.times == 0 {
			continue
		}
		if f.times > 0 {
			f.times--
		}
		return f
	}
	return nil
}

// route names the operation a request is, and the numeric id in its path if it
// carries one.
func route(r *http.Request) (string, int64) {
	p := r.URL.Path
	releases := "/repos/" + fakeRepo + "/releases"
	uploads := "/uploads" + releases + "/"
	switch {
	case strings.HasPrefix(p, uploads) && strings.HasSuffix(p, "/assets") && r.Method == http.MethodPost:
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(p, uploads), "/assets"), 10, 64)
		return "upload", id
	case p == releases && r.Method == http.MethodGet:
		return "list", 0
	case p == releases && r.Method == http.MethodPost:
		return "create", 0
	case p == releases+"/generate-notes" && r.Method == http.MethodPost:
		return "notes", 0
	case strings.HasPrefix(p, releases+"/assets/") && r.Method == http.MethodDelete:
		id, _ := strconv.ParseInt(strings.TrimPrefix(p, releases+"/assets/"), 10, 64)
		return "delete-asset", id
	case strings.HasPrefix(p, releases+"/"):
		id, err := strconv.ParseInt(strings.TrimPrefix(p, releases+"/"), 10, 64)
		if err != nil {
			return "", 0
		}
		switch r.Method {
		case http.MethodGet:
			return "get", id
		case http.MethodPatch:
			return "update", id
		}
	}
	return "", 0
}

func (fg *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	fg.mu.Lock()
	fg.requests = append(fg.requests, r.Method+" "+r.URL.Path)
	fg.mu.Unlock()

	if r.Header.Get("Authorization") != "Bearer test-token" {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	op, id := route(r)
	if op == "" {
		fg.t.Errorf("fake GitHub has no route for %s %s", r.Method, r.URL.Path)
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}

	publishing := false
	if op == "update" {
		var in struct {
			Draft *bool `json:"draft"`
		}
		publishing = json.Unmarshal(body, &in) == nil && in.Draft != nil && !*in.Draft
	}
	fg.mu.Lock()
	target := fg.find(id)
	if op == "delete-asset" {
		target = fg.owner(id)
	}
	if target != nil && (op == "update" || op == "upload" || op == "delete-asset") {
		fg.writes = append(fg.writes, writeRequest{Op: op, ID: target.ID, Tag: target.Tag, Draft: target.Draft, AfterPublish: len(fg.publishRequests) > 0})
		if publishing {
			req := publishRequest{ID: target.ID, Tag: target.Tag, Draft: target.Draft}
			for _, a := range target.Assets {
				req.Assets = append(req.Assets, *a)
			}
			fg.publishRequests = append(fg.publishRequests, req)
		}
	}
	fg.mu.Unlock()

	// A fault on "publish" matches only updates that publish; one on "update"
	// matches any update.
	var f *fault
	if publishing {
		f = fg.takeFault("publish")
	}
	if f == nil {
		f = fg.takeFault(op)
	}
	if f != nil && f.lie {
		if op == "upload" {
			fg.mu.Lock()
			aid := fg.id()
			fg.mu.Unlock()
			fg.writeJSON(w, http.StatusCreated, map[string]any{"id": aid, "name": r.URL.Query().Get("name"), "state": "uploaded", "size": len(body)})
			return
		}
		fg.writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	if f != nil && !f.after {
		fg.fail(w, r, id, f)
		return
	}
	rec := httptest.NewRecorder()
	fg.handle(rec, r, op, id, body)
	if f != nil {
		fg.fail(w, r, id, f)
		return
	}
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	w.Write(rec.Body.Bytes())
}

func (fg *fakeGitHub) fail(w http.ResponseWriter, r *http.Request, id int64, f *fault) {
	if f.starter {
		fg.mu.Lock()
		if rel := fg.find(id); rel != nil {
			rel.Assets = append(rel.Assets, &fakeAsset{ID: fg.id(), Name: r.URL.Query().Get("name"), State: "starter"})
		}
		fg.mu.Unlock()
	}
	if f.drop {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
		return
	}
	http.Error(w, `{"message":"Server Error"}`, f.status)
}

// owner is the release holding the asset with id.
func (fg *fakeGitHub) owner(assetID int64) *fakeRelease {
	for _, rel := range fg.releases {
		for _, a := range rel.Assets {
			if a.ID == assetID {
				return rel
			}
		}
	}
	return nil
}

func (fg *fakeGitHub) find(id int64) *fakeRelease {
	for _, rel := range fg.releases {
		if rel.ID == id {
			return rel
		}
	}
	return nil
}

func (fg *fakeGitHub) handle(w http.ResponseWriter, r *http.Request, op string, id int64, body []byte) {
	fg.mu.Lock()
	defer fg.mu.Unlock()

	switch op {
	case "list":
		fg.list(w, r)

	case "create":
		var in struct {
			TagName              string `json:"tag_name"`
			Draft                bool   `json:"draft"`
			GenerateReleaseNotes bool   `json:"generate_release_notes"`
		}
		if err := json.Unmarshal(body, &in); err != nil || in.TagName == "" {
			http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
			return
		}
		for _, rel := range fg.releases {
			if rel.Tag == in.TagName && !rel.Draft && !in.Draft {
				http.Error(w, `{"message":"Validation Failed","errors":[{"code":"already_exists"}]}`, http.StatusUnprocessableEntity)
				return
			}
		}
		rel := &fakeRelease{ID: fg.id(), Tag: in.TagName, Draft: in.Draft, unlisted: fg.listLag}
		if in.GenerateReleaseNotes {
			rel.Name, rel.Body = in.TagName, generatedNotes(in.TagName)
		}
		if fg.mangle == "create" {
			rel.Tag = untagged(rel.ID)
		}
		fg.releases = append(fg.releases, rel)
		fg.writeJSON(w, http.StatusCreated, fg.render(rel))

	case "get":
		rel := fg.find(id)
		if rel == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		out := fg.render(rel)
		if fg.mangle == "read" && rel.Draft {
			out["tag_name"] = untagged(rel.ID)
		}
		fg.writeJSON(w, http.StatusOK, out)

	case "update":
		rel := fg.find(id)
		if rel == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		var in struct {
			Name    *string `json:"name"`
			Body    *string `json:"body"`
			Draft   *bool   `json:"draft"`
			TagName *string `json:"tag_name"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, `{"message":"Problems parsing JSON"}`, http.StatusBadRequest)
			return
		}
		if in.Name != nil {
			rel.Name = *in.Name
		}
		if in.Body != nil {
			rel.Body = *in.Body
		}
		if in.Draft == nil && (fg.mangle == "notes" || fg.mangle == "unnamed-update" && in.TagName == nil) {
			rel.Tag = untagged(rel.ID)
		}
		out := fg.render(rel)
		if in.Draft != nil {
			if !*in.Draft {
				for _, other := range fg.releases {
					if other != rel && other.Tag == rel.Tag && !other.Draft {
						http.Error(w, `{"message":"Validation Failed","errors":[{"code":"already_exists"}]}`, http.StatusUnprocessableEntity)
						return
					}
				}
			}
			if fg.ignorePublish && !*in.Draft {
				out["draft"] = false
			} else {
				if rel.Draft && !*in.Draft {
					fg.published = append(fg.published, rel.ID)
					if fg.mangle == "publish" {
						rel.Tag = untagged(rel.ID)
					}
					if fg.mangle == "drop" && len(rel.Assets) > 0 {
						rel.Assets = rel.Assets[:len(rel.Assets)-1]
					}
				}
				rel.Draft = *in.Draft
				out = fg.render(rel)
			}
		}
		fg.writeJSON(w, http.StatusOK, out)

	case "notes":
		var in struct {
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal(body, &in); err != nil || in.TagName == "" {
			http.Error(w, `{"message":"Validation Failed"}`, http.StatusUnprocessableEntity)
			return
		}
		fg.writeJSON(w, http.StatusOK, map[string]string{"name": in.TagName, "body": generatedNotes(in.TagName)})

	case "delete-asset":
		for _, rel := range fg.releases {
			for i, a := range rel.Assets {
				if a.ID == id {
					rel.Assets = append(rel.Assets[:i], rel.Assets[i+1:]...)
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)

	case "upload":
		rel := fg.find(id)
		if rel == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		name := r.URL.Query().Get("name")
		if r.Header.Get("Content-Type") == "" {
			http.Error(w, `{"message":"Content-Type is required"}`, http.StatusBadRequest)
			return
		}
		if r.ContentLength != int64(len(body)) {
			http.Error(w, `{"message":"Content-Length does not match the body"}`, http.StatusBadRequest)
			return
		}
		for _, a := range rel.Assets {
			if a.Name == name {
				http.Error(w, `{"message":"Validation Failed","errors":[{"resource":"ReleaseAsset","code":"already_exists","field":"name"}]}`, http.StatusUnprocessableEntity)
				return
			}
		}
		a := &fakeAsset{ID: fg.id(), Name: name, State: "uploaded", ContentType: r.Header.Get("Content-Type"), Data: body}
		rel.Assets = append(rel.Assets, a)
		fg.writeJSON(w, http.StatusCreated, renderAsset(a))
	}
}

// list answers newest first, a page at a time, with a Link header to the next page:
// the shape GitHub lists releases in.
func (fg *fakeGitHub) list(w http.ResponseWriter, r *http.Request) {
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage <= 0 || perPage > 100 {
		perPage = 30
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	var all []*fakeRelease
	for _, rel := range fg.releases {
		if page == 1 && rel.unlisted > 0 {
			rel.unlisted--
			continue
		}
		if rel.vanishAfter > 0 {
			if rel.listed >= rel.vanishAfter {
				continue
			}
			if page == 1 {
				rel.listed++
			}
		}
		all = append(all, rel)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })

	start := (page - 1) * perPage
	end := min(start+perPage, len(all))
	out := []map[string]any{}
	for i := start; i < end; i++ {
		out = append(out, fg.render(all[i]))
	}
	if end < len(all) {
		next := fmt.Sprintf("%s%s?per_page=%d&page=%d", fg.srv.URL, r.URL.Path, perPage, page+1)
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, next))
	}
	fg.writeJSON(w, http.StatusOK, out)
}

func (fg *fakeGitHub) render(rel *fakeRelease) map[string]any {
	assets := []map[string]any{}
	for _, a := range rel.Assets {
		assets = append(assets, renderAsset(a))
	}
	return map[string]any{
		"id":         rel.ID,
		"tag_name":   rel.Tag,
		"name":       rel.Name,
		"body":       rel.Body,
		"draft":      rel.Draft,
		"prerelease": rel.Prerelease,
		"upload_url": fmt.Sprintf("%s/uploads/repos/%s/releases/%d/assets{?name,label}", fg.srv.URL, fakeRepo, rel.ID),
		"assets":     assets,
	}
}

func renderAsset(a *fakeAsset) map[string]any {
	out := map[string]any{
		"id":           a.ID,
		"name":         a.Name,
		"state":        a.State,
		"size":         len(a.Data),
		"content_type": a.ContentType,
		"digest":       nil,
	}
	if a.State == "uploaded" {
		out["digest"] = fmt.Sprintf("sha256:%x", sha256.Sum256(a.Data))
	}
	return out
}

func (fg *fakeGitHub) writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		fg.t.Errorf("fake GitHub could not encode its answer: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func generatedNotes(tag string) string {
	return "**Full Changelog**: https://github.com/" + fakeRepo + "/commits/" + tag
}
