// Package releasepublish publishes the GitHub release for a tag so that running it
// again finishes whatever an earlier run left behind.
//
// It exists because `gh release create` cannot be repeated. With assets it creates a
// draft, uploads to it and then publishes it, deleting the draft if an upload fails.
// On 2026-09-13 GitHub was failing writes to releases: the create answered 500, the
// draft was made anyway, the delete that was meant to clean it up failed too, and
// every rerun after that was refused. A draft is also invisible to
// GET /releases/tags/{tag}, so looking for the release by its tag reported that there
// was none.
//
// Publish therefore never acts on what it sent, only on what GitHub holds. Each pass
// reads the release list, finishes whatever stage the release is at, and reads the
// release back; a failure GitHub may have acted on is followed by a fresh pass rather
// than by sending the same request again. Repeating a create is how releases get
// duplicated. Repeating a pass is not.
package releasepublish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultWaits are the pauses between passes after a failure that may clear up: a
// little under seven minutes in all, spread over ten retries. That covers the blips
// that should not cost a build whose notarization took minutes of Apple's queue, and
// stays well inside the release job's timeout. An outage that lasts longer ends the
// run red, and a rerun later picks the release up where this one stopped.
var DefaultWaits = []time.Duration{
	5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second,
	60 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second,
	60 * time.Second, 60 * time.Second,
}

const (
	apiTimeout    = 2 * time.Minute
	uploadTimeout = 20 * time.Minute

	stateUploaded = "uploaded"
)

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Publisher publishes releases in one repository.
type Publisher struct {
	// API is the REST API root, https://api.github.com for github.com.
	API string
	// Repo is owner/name.
	Repo  string
	Token string

	Client *http.Client
	// Log receives progress lines, and GitHub Actions workflow commands
	// (::warning::) for what a person should see on the run's summary.
	Log io.Writer
	// Waits are the pauses before each retry. Their number is the number of
	// retries; none means a single pass.
	Waits []time.Duration
	// Sleep waits between passes; nil means a real wait that a cancelled context
	// cuts short.
	Sleep func(context.Context, time.Duration) error
}

// APIError is GitHub answering a request with an error status.
type APIError struct {
	Method  string
	URL     string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, e.Message)
}

// transportError is a request whose outcome is unknown: the connection failed, timed
// out, or the answer could not be read.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

// unfinishedError is a release that did not read back the way the pass left it.
type unfinishedError struct{ problems []string }

func (e *unfinishedError) Error() string {
	return "the release does not read back as finished: " + strings.Join(e.problems, "; ")
}

type artifact struct {
	path   string
	name   string
	size   int64
	digest string
}

type release struct {
	ID         int64   `json:"id"`
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Body       string  `json:"body"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	HTMLURL    string  `json:"html_url"`
	UploadURL  string  `json:"upload_url"`
	Assets     []asset `json:"assets"`
}

type asset struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	State  string  `json:"state"`
	Size   int64   `json:"size"`
	Digest *string `json:"digest"`
}

// expectation is what an asset must read back as once a pass is done. An empty
// digest is not checked.
type expectation struct {
	size   int64
	digest string
}

// Publish makes the release for tag exist, carry generated notes and every one of
// paths, and be published, starting from whatever state the release is in. It
// returns nil only after reading the release back in that state.
func (p *Publisher) Publish(ctx context.Context, tag string, paths []string) error {
	switch {
	case tag == "":
		return errors.New("no tag to publish")
	case !repoPattern.MatchString(p.Repo):
		return fmt.Errorf("repository %q is not owner/name", p.Repo)
	case p.Token == "":
		return errors.New("no token to publish with")
	case len(paths) == 0:
		return errors.New("no artifacts to publish")
	}
	// Every file is read before GitHub is touched: a build that did not produce what
	// the release needs must not leave a release behind.
	arts, err := readArtifacts(paths)
	if err != nil {
		return fmt.Errorf("release %s was not published: %w", tag, err)
	}

	warned := map[int64]bool{}
	uncertain := false
	for attempt := 1; ; attempt++ {
		err := p.pass(ctx, tag, arts, warned)
		if err == nil {
			return nil
		}
		if !retryable(ctx, err, uncertain) {
			return fmt.Errorf("release %s was not published: %w", tag, err)
		}
		if attempt > len(p.Waits) {
			return fmt.Errorf("release %s was not published after %d attempts: %w", tag, attempt, err)
		}
		// From here on an earlier request may have landed without an answer.
		uncertain = true
		wait := p.Waits[attempt-1]
		p.warnf("attempt %d of %d to publish %s failed, trying again in %s: %v", attempt, len(p.Waits)+1, tag, wait, err)
		if err := p.sleep(ctx, wait); err != nil {
			return fmt.Errorf("release %s was not published: %w", tag, err)
		}
	}
}

// retryable says whether another pass can succeed where this one failed. A refusal is
// final: GitHub gives the same answer minutes later, and waiting only hides it. A 422
// is a refusal too, unless an earlier attempt may have done the thing it now refuses
// to do twice.
func retryable(ctx context.Context, err error, uncertain bool) bool {
	if ctx.Err() != nil {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status >= 500, apiErr.Status == http.StatusTooManyRequests:
			return true
		case apiErr.Status == http.StatusUnprocessableEntity:
			return uncertain
		}
		return false
	}
	var transport *transportError
	var unfinished *unfinishedError
	return errors.As(err, &transport) || errors.As(err, &unfinished)
}

func (p *Publisher) pass(ctx context.Context, tag string, arts []artifact, warned map[int64]bool) error {
	all, err := p.listReleases(ctx)
	if err != nil {
		return err
	}
	var published, drafts []release
	for _, r := range all {
		switch {
		case r.TagName != tag:
		case r.Draft:
			drafts = append(drafts, r)
		default:
			published = append(published, r)
		}
	}
	sort.Slice(drafts, func(i, j int) bool { return drafts[i].ID < drafts[j].ID })

	var rel release
	switch {
	case len(published) > 1:
		return fmt.Errorf("%d published releases carry the tag %s; which of them is the release is for a person to decide", len(published), tag)
	case len(published) == 1:
		rel = published[0]
		p.logf("release %d for %s is already published; checking it is complete", rel.ID, tag)
	case len(drafts) > 1:
		// Nothing says which of several drafts is this release, and picking one
		// publishes whatever that one carries, which cannot be taken back.
		ids := make([]string, len(drafts))
		for i, d := range drafts {
			ids[i] = strconv.FormatInt(d.ID, 10)
		}
		return fmt.Errorf("%d draft releases carry the tag %s (%s) and nothing says which of them is this release; delete the ones that are not and run again", len(drafts), tag, strings.Join(ids, ", "))
	case len(drafts) == 1:
		rel = drafts[0]
		if err := p.requireOurs(ctx, rel, arts); err != nil {
			return err
		}
		p.logf("finishing draft release %d for %s", rel.ID, tag)
	default:
		p.logf("creating a draft release for %s", tag)
		if err := p.call(ctx, http.MethodPost, p.repoURL("/releases"), map[string]any{
			"tag_name":               tag,
			"draft":                  true,
			"generate_release_notes": true,
		}, &rel); err != nil {
			return err
		}
	}
	// Drafts beside a published release are not this release and are not published;
	// they are left alone, since deleting is not this program's call, and named where a
	// person sees them.
	for _, d := range drafts {
		if d.ID != rel.ID && !warned[d.ID] {
			warned[d.ID] = true
			p.warnf("draft release %d also carries the tag %s and was left as it is; it can be deleted once release %d is published", d.ID, tag, rel.ID)
		}
	}

	// A draft made by hand, or by gh before its upload failed, has no notes.
	if rel.Body == "" {
		if err := p.writeNotes(ctx, &rel); err != nil {
			return err
		}
	}

	expect := map[string]expectation{}
	for _, a := range arts {
		have := findAsset(rel.Assets, a.name)
		switch {
		case have != nil && have.State == stateUploaded && have.holds(a):
			// Already this build's bytes, uploaded by an earlier pass or run.
		case have != nil && have.State == stateUploaded && !rel.Draft:
			// A published file is not swapped for a rebuilt one: people may have
			// downloaded it, and the updater would briefly find nothing at its address.
			p.logf("%s is already published on %s and stays as it is", a.name, tag)
			expect[a.name] = expectation{size: have.Size, digest: deref(have.Digest)}
			continue
		default:
			if have != nil {
				p.logf("removing %s (state %q) left by an earlier attempt", a.name, have.State)
				if err := p.call(ctx, http.MethodDelete, p.repoURL(fmt.Sprintf("/releases/assets/%d", have.ID)), nil, nil); err != nil {
					return err
				}
			}
			p.logf("uploading %s (%d bytes)", a.name, a.size)
			if err := p.upload(ctx, rel, a); err != nil {
				return err
			}
		}
		expect[a.name] = expectation{size: a.size, digest: a.digest}
	}

	if rel.Draft {
		p.logf("publishing release %d for %s", rel.ID, tag)
		if err := p.call(ctx, http.MethodPatch, p.repoURL(fmt.Sprintf("/releases/%d", rel.ID)), map[string]any{"draft": false}, nil); err != nil {
			return err
		}
	}

	// What the requests answered is not evidence of anything; the release as GitHub
	// now holds it is.
	var got release
	if err := p.call(ctx, http.MethodGet, p.repoURL(fmt.Sprintf("/releases/%d", rel.ID)), nil, &got); err != nil {
		return err
	}
	if err := check(got, tag, arts, expect); err != nil {
		return err
	}
	p.logf("published %s: %s", tag, got.HTMLURL)
	return nil
}

func check(got release, tag string, arts []artifact, expect map[string]expectation) error {
	var problems []string
	if got.TagName != tag {
		problems = append(problems, fmt.Sprintf("it carries the tag %q", got.TagName))
	}
	if got.Draft {
		problems = append(problems, "it is still a draft")
	}
	if got.Body == "" {
		problems = append(problems, "it has no notes")
	}
	for _, a := range arts {
		want := expect[a.name]
		have := findAsset(got.Assets, a.name)
		switch {
		case have == nil:
			problems = append(problems, a.name+" is missing")
		case have.State != stateUploaded:
			problems = append(problems, fmt.Sprintf("%s is in state %q", a.name, have.State))
		case have.Size != want.size:
			problems = append(problems, fmt.Sprintf("%s holds %d bytes, want %d", a.name, have.Size, want.size))
		case want.digest != "" && have.Digest != nil && *have.Digest != want.digest:
			problems = append(problems, fmt.Sprintf("%s has digest %s, want %s", a.name, *have.Digest, want.digest))
		}
	}
	if len(problems) > 0 {
		return &unfinishedError{problems: problems}
	}
	return nil
}

type notes struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// generateNotes asks GitHub for the title and notes it would write for tag. It
// changes nothing.
func (p *Publisher) generateNotes(ctx context.Context, tag string) (notes, error) {
	var n notes
	err := p.call(ctx, http.MethodPost, p.repoURL("/releases/generate-notes"), map[string]any{"tag_name": tag}, &n)
	return n, err
}

// requireOurs refuses a draft that carries anything this publish would not have put
// there itself: publishing it would publish that too. A draft passes when it is not a
// prerelease, its title and notes are empty or exactly the ones GitHub generates for
// the tag, and every file on it is one this build makes. That is the shape a draft
// has when this program, or gh before it, made it and was cut off; anything else was
// shaped by a person, and a person decides what happens to it.
func (p *Publisher) requireOurs(ctx context.Context, rel release, arts []artifact) error {
	var foreign []string
	if rel.Prerelease {
		foreign = append(foreign, "it is marked as a prerelease")
	}
	for _, a := range rel.Assets {
		if !makes(arts, a.Name) {
			foreign = append(foreign, "it holds "+a.Name+", which this build does not make")
		}
	}
	if rel.Name != "" || rel.Body != "" {
		generated, err := p.generateNotes(ctx, rel.TagName)
		if err != nil {
			return err
		}
		if rel.Name != "" && rel.Name != generated.Name {
			foreign = append(foreign, fmt.Sprintf("its title %q is not the generated %q", rel.Name, generated.Name))
		}
		if rel.Body != "" && rel.Body != generated.Body {
			foreign = append(foreign, "its notes are not the ones GitHub generates for the tag")
		}
	}
	if len(foreign) > 0 {
		return fmt.Errorf("draft release %d for %s was not made by a publish and would be published as it is: %s; fix or delete it and run again", rel.ID, rel.TagName, strings.Join(foreign, "; "))
	}
	return nil
}

func makes(arts []artifact, name string) bool {
	for _, a := range arts {
		if a.name == name {
			return true
		}
	}
	return false
}

func (p *Publisher) writeNotes(ctx context.Context, rel *release) error {
	p.logf("generating notes for release %d", rel.ID)
	notes, err := p.generateNotes(ctx, rel.TagName)
	if err != nil {
		return err
	}
	change := map[string]any{"body": notes.Body}
	if rel.Name == "" {
		change["name"] = notes.Name
	}
	return p.call(ctx, http.MethodPatch, p.repoURL(fmt.Sprintf("/releases/%d", rel.ID)), change, rel)
}

func (p *Publisher) upload(ctx context.Context, rel release, a artifact) error {
	base, _, _ := strings.Cut(rel.UploadURL, "{")
	if base == "" {
		return fmt.Errorf("release %d has no upload address", rel.ID)
	}
	f, err := os.Open(a.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = p.do(ctx, uploadTimeout, http.MethodPost, base+"?name="+url.QueryEscape(a.name), f, a.size, contentType(a.name), nil)
	return err
}

func (p *Publisher) listReleases(ctx context.Context) ([]release, error) {
	var all []release
	next := p.repoURL("/releases?per_page=100")
	for next != "" {
		var page []release
		header, err := p.do(ctx, apiTimeout, http.MethodGet, next, nil, 0, "", &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		next = nextLink(header.Get("Link"))
		// The token goes wherever the next page is; it goes nowhere but the API.
		if next != "" && !strings.HasPrefix(next, p.apiRoot()+"/") {
			return nil, fmt.Errorf("the release list points its next page outside %s: %s", p.apiRoot(), next)
		}
	}
	return all, nil
}

// call sends in as JSON, if there is one, and decodes the answer into out, if asked.
func (p *Publisher) call(ctx context.Context, method, u string, in, out any) error {
	var body io.Reader
	var size int64
	var ct string
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, size, ct = bytes.NewReader(data), int64(len(data)), "application/json"
	}
	_, err := p.do(ctx, apiTimeout, method, u, body, size, ct, out)
	return err
}

func (p *Publisher) do(ctx context.Context, timeout time.Duration, method, u string, body io.Reader, size int64, ct string, out any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.ContentLength = size
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "fleetdeck-publish-release")

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &transportError{fmt.Errorf("%s %s: %w", method, u, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &transportError{fmt.Errorf("%s %s: reading the answer: %w", method, u, err)}
	}
	if resp.StatusCode >= 300 {
		return nil, &APIError{Method: method, URL: u, Status: resp.StatusCode, Message: message(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, &transportError{fmt.Errorf("%s %s: unreadable answer: %w", method, u, err)}
		}
	}
	return resp.Header, nil
}

func (p *Publisher) apiRoot() string { return strings.TrimSuffix(p.API, "/") }

func (p *Publisher) repoURL(path string) string {
	return p.apiRoot() + "/repos/" + p.Repo + path
}

func (p *Publisher) sleep(ctx context.Context, d time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (p *Publisher) logf(format string, args ...any) {
	if p.Log != nil {
		// A log line that cannot be written is not a reason to stop a publish.
		_, _ = fmt.Fprintln(p.Log, oneLine(fmt.Sprintf(format, args...)))
	}
}

func (p *Publisher) warnf(format string, args ...any) {
	p.logf("::warning::"+format, args...)
}

func readArtifacts(paths []string) ([]artifact, error) {
	seen := map[string]string{}
	arts := make([]artifact, 0, len(paths))
	for _, path := range paths {
		a, err := readArtifact(path)
		if err != nil {
			return nil, err
		}
		if other, dup := seen[a.name]; dup {
			return nil, fmt.Errorf("%s and %s would both be published as %s", other, path, a.name)
		}
		seen[a.name] = path
		arts = append(arts, a)
	}
	return arts, nil
}

func readArtifact(path string) (artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return artifact{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return artifact{}, err
	}
	if !info.Mode().IsRegular() {
		return artifact{}, fmt.Errorf("%s is not a regular file", path)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return artifact{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return artifact{path: path, name: filepath.Base(path), size: n, digest: "sha256:" + hex.EncodeToString(h.Sum(nil))}, nil
}

// contentType is the type every release so far carried for each kind of artifact,
// as gh chose them.
func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		return "application/x-gtar"
	case strings.HasSuffix(name, ".zip"):
		return "application/zip"
	case strings.HasSuffix(name, ".dmg"):
		return "application/x-apple-diskimage"
	}
	return "application/octet-stream"
}

func findAsset(assets []asset, name string) *asset {
	for i := range assets {
		if assets[i].Name == name {
			return &assets[i]
		}
	}
	return nil
}

func (a *asset) holds(art artifact) bool {
	return a.Size == art.size && a.Digest != nil && *a.Digest == art.digest
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nextLink is the rel="next" address in a Link header, or "".
func nextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if ok && strings.Contains(params, `rel="next"`) {
			return strings.Trim(strings.TrimSpace(target), "<>")
		}
	}
	return ""
}

// message is GitHub's own explanation from an error body, kept to one line.
func message(data []byte) string {
	var parsed struct {
		Message string          `json:"message"`
		Errors  json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(data, &parsed); err == nil && parsed.Message != "" {
		if len(parsed.Errors) > 0 {
			return parsed.Message + " " + string(parsed.Errors)
		}
		return parsed.Message
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// oneLine keeps a message on one line, which a workflow command needs to be read as
// one.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
