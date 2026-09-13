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
}

type fakeRelease struct {
	ID     int64
	Tag    string
	Name   string
	Body   string
	Draft  bool
	Assets []*fakeAsset
}

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
		Sleep:  func(context.Context, time.Duration) error { return nil },
	}
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
func (fg *fakeGitHub) mutations() []string {
	fg.mu.Lock()
	defer fg.mu.Unlock()
	var out []string
	for _, r := range fg.requests {
		if !strings.HasPrefix(r, "GET ") {
			out = append(out, r)
		}
	}
	return out
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

	f := fg.takeFault(op)
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
		rel := &fakeRelease{ID: fg.id(), Tag: in.TagName, Draft: in.Draft}
		if in.GenerateReleaseNotes {
			rel.Name, rel.Body = in.TagName, generatedNotes(in.TagName)
		}
		fg.releases = append(fg.releases, rel)
		fg.writeJSON(w, http.StatusCreated, fg.render(rel))

	case "get":
		rel := fg.find(id)
		if rel == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		fg.writeJSON(w, http.StatusOK, fg.render(rel))

	case "update":
		rel := fg.find(id)
		if rel == nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		var in struct {
			Name  *string `json:"name"`
			Body  *string `json:"body"`
			Draft *bool   `json:"draft"`
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
	all := make([]*fakeRelease, len(fg.releases))
	copy(all, fg.releases)
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
