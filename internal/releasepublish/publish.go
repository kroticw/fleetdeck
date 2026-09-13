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
//
// Running this against a sandbox repository on 2026-09-13 showed two more things.
//
// The release list trails a create by 0.7 to 2.2 seconds, so a release this run has
// just made may not be in it yet.
//
// And a release can end up under a tag named untagged-... instead of its own. One
// cause is established: an update of a draft that does not publish it and does not
// name tag_name comes back with the draft's tag replaced, every time it was tried,
// while the same update naming tag_name keeps the tag and an update that publishes
// keeps it without naming it. Two releases were published that way in the sandbox,
// by an earlier version of this program that wrote notes without the tag and whose
// retries did not yet stop at a wrong tag. The v0.8.0 draft on kroticw/fleetdeck
// that showed untagged-... was probably the same rule: before that was seen, an
// update carrying only a name and no tag_name went to it and was answered with a 5xx,
// and whether that update was carried out was not checked; the other draft, which no
// update touched, kept v0.8.0. So updates name the tag, and the tag is still checked
// rather than trusted: on the release found, after writing notes, on the release
// itself just before a publish, and after it. Once a publish request has gone out,
// nothing is written again at all, not even to that release: it is only read.
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
// little under seven minutes of pauses, spread over ten retries, on top of the time
// the requests themselves take. DefaultDeadline bounds the whole. An outage that lasts
// longer ends the run red, and a rerun of the publish picks the release up where this
// one stopped.
var DefaultWaits = []time.Duration{
	5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second,
	60 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second,
	60 * time.Second, 60 * time.Second,
}

// DefaultSettle are the pauses between readings of the release list after a create
// that may have gone through without an answer, before creating again. The list was
// measured trailing a create by 0.7 to 2.2 seconds on 2026-09-13; this waits for up to
// half a minute, an order of magnitude more, because creating again too early is how
// a second draft is born.
var DefaultSettle = []time.Duration{
	1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second,
}

// DefaultDeadline is how long a whole publish may take, retries and uploads included.
// It is well under the publish job's own timeout, so that a publish that runs out of
// time ends with an error saying the release was not published, rather than with a
// job GitHub cancelled in the middle of a request.
const DefaultDeadline = 20 * time.Minute

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
	// Settle are the pauses between readings of the release list after a create
	// whose answer was lost; see DefaultSettle.
	Settle []time.Duration
	// Sleep waits between passes and readings; nil means a real wait that a
	// cancelled context cuts short.
	Sleep func(context.Context, time.Duration) error
	// Deadline is how long Publish may take in all; zero means no limit of its own.
	Deadline time.Duration
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
type transportError struct {
	method string
	err    error
}

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

// expectations are the size and digest of each of this build's files.
func expectations(arts []artifact) map[string]expectation {
	expect := make(map[string]expectation, len(arts))
	for _, a := range arts {
		expect[a.name] = expectation{size: a.size, digest: a.digest}
	}
	return expect
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

	parent := ctx
	if p.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.Deadline)
		defer cancel()
	}
	outOfTime := func(err error) error {
		return fmt.Errorf("release %s was not published as it should be within %s, the time a publish is given: %w", tag, p.Deadline, err)
	}

	st := &runState{warned: map[int64]bool{}, digests: map[int64]string{}}
	for attempt := 1; ; attempt++ {
		err := p.pass(ctx, tag, arts, st)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil && parent.Err() == nil {
			return outOfTime(err)
		}
		if !retryable(ctx, err, st.uncertain) {
			return fmt.Errorf("release %s was not published as it should be: %w", tag, err)
		}
		if attempt > len(p.Waits) {
			return fmt.Errorf("release %s was not published as it should be after %d attempts: %w", tag, attempt, err)
		}
		// From here on a request that could change something may have landed without
		// an answer. A failed read changes nothing and leaves nothing to be unsure of.
		if uncertainWrite(err) {
			st.uncertain = true
		}
		wait := p.Waits[attempt-1]
		p.warnf("attempt %d of %d to publish %s failed, trying again in %s: %v", attempt, len(p.Waits)+1, tag, wait, err)
		if serr := p.sleep(ctx, wait); serr != nil {
			if ctx.Err() != nil && parent.Err() == nil {
				return outOfTime(err)
			}
			return fmt.Errorf("release %s was not published: %w", tag, serr)
		}
	}
}

// uncertainWrite says whether err is a request that could have changed something and
// whose outcome is unknown.
func uncertainWrite(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Method != http.MethodGet && (apiErr.Status >= 500 || apiErr.Status == http.StatusTooManyRequests)
	}
	var transport *transportError
	return errors.As(err, &transport) && transport.method != http.MethodGet
}

// retryable says whether another pass can succeed where this one failed. A refusal is
// final: GitHub gives the same answer minutes later, and waiting only hides it. A 422
// is a refusal too, unless an earlier write may have done the thing it now refuses to
// do twice.
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

// stopError ends a publish at once: retryable never retries it. Something is wrong
// that another attempt cannot put right, and after a publish another attempt can only
// make it worse: publishing cannot be taken back, and on 2026-09-13 retrying after a
// release came back wrong published two more.
type stopError struct{ msg string }

func (e *stopError) Error() string { return e.msg }

func stopf(format string, args ...any) error {
	return &stopError{msg: fmt.Sprintf(format, args...)}
}

// runState is what one run of Publish remembers between its passes.
type runState struct {
	warned map[int64]bool
	// uncertain: an earlier write failed in a way GitHub may have acted on.
	uncertain bool
	// digests are the sha256 digests of assets GitHub gave none for, by asset id,
	// computed from their downloaded bytes. An asset id names fixed content: a
	// replaced file gets a new one.
	digests map[int64]string
	// created is the release a create of this run was answered with. The release
	// list trails a create by a second or two, and this is how the release is found
	// in that time without making another.
	created int64
	// createUncertain: a create was sent and its answer never came back.
	createUncertain bool
	// pinned is the release a publish request has gone out for, or that was found
	// already published. From then on no other release is created, finished or
	// published by this run.
	pinned int64
	// publishSent: a publish request has gone out. Nothing at all is written after
	// that.
	publishSent bool
	// keptWarned: the files of a published release that are not this build's
	// bytes have been named.
	keptWarned bool
	// sawPublished: a pass has already found the pinned release published. The next
	// one only reads it.
	sawPublished bool
}

func (p *Publisher) pass(ctx context.Context, tag string, arts []artifact, st *runState) error {
	var rel release
	if st.pinned != 0 {
		if err := p.call(ctx, http.MethodGet, p.releaseURL(st.pinned), nil, &rel); err != nil {
			return err
		}
	} else {
		var err error
		if rel, err = p.find(ctx, tag, arts, st); err != nil {
			return err
		}
	}
	if err := requireTag(rel, tag); err != nil {
		return err
	}

	if st.publishSent {
		// A publish request has gone out for this release. Whatever it did, nothing
		// more is written, not even to this release: a read can show a release that
		// was published as a draft, and a pass acting on that read could delete a
		// public file. The release is read until it reads back published, and if it
		// never does the run ends red and a rerun of the publish takes it from there.
		return p.confirm(ctx, rel, tag, arts, expectations(arts), st)
	}

	// A release found published may get the files it is missing, once. After that it
	// is only read: writing to something already public again is a retry after
	// publication.
	writes := true
	if !rel.Draft {
		writes = !st.sawPublished
		st.sawPublished = true
		st.pinned = rel.ID
		p.logf("release %d for %s is published; checking it is complete", rel.ID, tag)
	}

	// A draft made by hand, or by gh before its upload failed, has no notes.
	if rel.Draft && rel.Body == "" {
		if err := p.writeNotes(ctx, &rel); err != nil {
			return err
		}
		if err := requireTag(rel, tag); err != nil {
			return err
		}
	}

	expect := map[string]expectation{}
	var kept []string
	for _, a := range arts {
		have := findAsset(rel.Assets, a.name)
		same := false
		if have != nil && have.State == stateUploaded && have.Size == a.size {
			digest, err := p.digestOf(ctx, *have, st)
			if err != nil {
				return err
			}
			same = digest == a.digest
		}
		switch {
		case same:
			// Already this build's bytes, uploaded by an earlier pass or run.
		case have != nil && have.State == stateUploaded && !rel.Draft:
			// A published file is not swapped for a rebuilt one: people may have
			// downloaded it, and the updater would briefly find nothing at its address.
			kept = append(kept, a.name)
			expect[a.name] = expectation{size: have.Size}
			continue
		case !writes:
			// Reported by the check below.
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
	if len(kept) > 0 && !st.keptWarned {
		st.keptWarned = true
		p.warnf("release %d for %s was already published and keeps its own %s, which are not the bytes of this build; this build's were not uploaded", rel.ID, tag, strings.Join(kept, ", "))
	}

	if rel.Draft {
		// The last look before the one step that cannot be taken back, at the release
		// itself rather than at what earlier answers said about it.
		var fresh release
		if err := p.call(ctx, http.MethodGet, p.releaseURL(rel.ID), nil, &fresh); err != nil {
			return err
		}
		if err := requireTag(fresh, tag); err != nil {
			return err
		}
		if fresh.Draft {
			problems, err := p.contentProblems(ctx, fresh, arts, expect, st)
			if err != nil {
				return err
			}
			if len(problems) > 0 {
				return &unfinishedError{problems: problems}
			}
			st.pinned = rel.ID
			st.publishSent = true
			p.logf("publishing release %d for %s", rel.ID, tag)
			if err := p.call(ctx, http.MethodPatch, p.releaseURL(rel.ID), map[string]any{"draft": false}, nil); err != nil {
				return err
			}
		}
	}

	// What the requests answered is not evidence of anything; the release as GitHub
	// now holds it is.
	var got release
	if err := p.call(ctx, http.MethodGet, p.releaseURL(rel.ID), nil, &got); err != nil {
		return err
	}
	if err := requireTag(got, tag); err != nil {
		return err
	}
	return p.confirm(ctx, got, tag, arts, expect, st)
}

// confirm is the last word on a release: published with everything expected of it,
// still a draft, which another reading may change, or published and wrong, which
// nothing this program sends can put right.
func (p *Publisher) confirm(ctx context.Context, got release, tag string, arts []artifact, expect map[string]expectation, st *runState) error {
	if got.Draft {
		return &unfinishedError{problems: []string{fmt.Sprintf("release %d still reads as a draft; after a publish request it is only read, and a rerun of the publish takes it from here", got.ID)}}
	}
	problems, err := p.contentProblems(ctx, got, arts, expect, st)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return stopf("release %d for %s is published but not right, and nothing more is sent to it: %s", got.ID, tag, strings.Join(problems, "; "))
	}
	p.logf("published %s: %s", tag, got.HTMLURL)
	return nil
}

// find is the release for tag as the release list shows it, or a new draft when there
// is none.
func (p *Publisher) find(ctx context.Context, tag string, arts []artifact, st *runState) (release, error) {
	published, drafts, err := p.onTag(ctx, tag)
	if err != nil {
		return release{}, err
	}
	if len(published)+len(drafts) == 0 && st.created != 0 {
		var rel release
		if err := p.call(ctx, http.MethodGet, p.releaseURL(st.created), nil, &rel); err != nil {
			return release{}, err
		}
		p.logf("release %d, created by this run, is not in the release list yet; reading it by its id", rel.ID)
		return rel, nil
	}
	if len(published)+len(drafts) == 0 && st.createUncertain {
		for _, wait := range p.Settle {
			p.logf("a create may have gone through without an answer; reading the release list again in %s", wait)
			if err := p.sleep(ctx, wait); err != nil {
				return release{}, err
			}
			if published, drafts, err = p.onTag(ctx, tag); err != nil {
				return release{}, err
			}
			if len(published)+len(drafts) > 0 {
				break
			}
		}
	}

	var rel release
	switch {
	case len(published) > 1:
		return release{}, stopf("%d published releases carry the tag %s; which of them is the release is for a person to decide", len(published), tag)
	case len(published) == 1:
		rel = published[0]
	case len(drafts) > 1:
		// Nothing says which of several drafts is this release, and picking one
		// publishes whatever that one carries, which cannot be taken back.
		ids := make([]string, len(drafts))
		for i, d := range drafts {
			ids[i] = strconv.FormatInt(d.ID, 10)
		}
		return release{}, stopf("%d draft releases carry the tag %s (%s) and nothing says which of them is this release; delete the ones that are not and run again", len(drafts), tag, strings.Join(ids, ", "))
	case len(drafts) == 1:
		rel = drafts[0]
		if err := p.requireOurs(ctx, rel, arts); err != nil {
			return release{}, err
		}
		p.logf("finishing draft release %d for %s", rel.ID, tag)
	default:
		p.logf("creating a draft release for %s", tag)
		st.createUncertain = true
		if err := p.call(ctx, http.MethodPost, p.repoURL("/releases"), map[string]any{
			"tag_name":               tag,
			"draft":                  true,
			"generate_release_notes": true,
		}, &rel); err != nil {
			return release{}, err
		}
		st.createUncertain = false
		st.created = rel.ID
	}
	// Drafts beside a published release are not this release and are not published;
	// they are left alone, since deleting is not this program's call, and named where a
	// person sees them.
	for _, d := range drafts {
		if d.ID != rel.ID && !st.warned[d.ID] {
			st.warned[d.ID] = true
			p.warnf("draft release %d also carries the tag %s and was left as it is; it can be deleted once release %d is published", d.ID, tag, rel.ID)
		}
	}
	return rel, nil
}

// onTag reads the release list and returns the releases carrying tag, drafts oldest
// first.
func (p *Publisher) onTag(ctx context.Context, tag string) (published, drafts []release, err error) {
	all, err := p.listReleases(ctx)
	if err != nil {
		return nil, nil, err
	}
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
	return published, drafts, nil
}

// requireTag stops at a release carrying any tag but ours. GitHub has been seen to
// give a release a tag named untagged-... in place of the one asked for, and a
// release with the wrong tag is somebody else's problem to publish, or already a
// mistake in public.
func requireTag(rel release, tag string) error {
	switch {
	case rel.TagName == tag:
		return nil
	case rel.Draft:
		return stopf("draft release %d carries the tag %q, not %s, and was not published; a person has to look at it", rel.ID, rel.TagName, tag)
	default:
		return stopf("release %d was published with the tag %q instead of %s; nothing more was sent, and a person has to look at it", rel.ID, rel.TagName, tag)
	}
}

// contentProblems is what a release lacks: notes, and each artifact uploaded with the
// size and digest expected of it. A matching size says nothing about the bytes, so a
// file GitHub gives no digest for is downloaded and hashed.
func (p *Publisher) contentProblems(ctx context.Context, got release, arts []artifact, expect map[string]expectation, st *runState) ([]string, error) {
	var problems []string
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
		case want.digest != "":
			digest, err := p.digestOf(ctx, *have, st)
			if err != nil {
				return nil, err
			}
			if digest != want.digest {
				problems = append(problems, fmt.Sprintf("%s has digest %s, want %s", a.name, digest, want.digest))
			}
		}
	}
	return problems, nil
}

// digestOf is the sha256 digest of an uploaded asset: the one GitHub gives, or, when
// it gives none, the digest of the bytes it serves.
func (p *Publisher) digestOf(ctx context.Context, a asset, st *runState) (string, error) {
	if a.Digest != nil {
		return *a.Digest, nil
	}
	if digest, ok := st.digests[a.ID]; ok {
		return digest, nil
	}
	p.logf("GitHub gives no digest for %s; downloading it to check its bytes", a.Name)
	digest, err := p.download(ctx, a.ID)
	if err != nil {
		return "", err
	}
	st.digests[a.ID] = digest
	return digest, nil
}

// download hashes an asset's bytes as GitHub serves them. The API answers with a
// redirect to where the bytes are kept; the client follows it without the token, which
// net/http does not send to another host.
func (p *Publisher) download(ctx context.Context, id int64) (string, error) {
	u := p.repoURL(fmt.Sprintf("/releases/assets/%d", id))
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "fleetdeck-publish-release")
	resp, err := p.client().Do(req)
	if err != nil {
		return "", &transportError{method: http.MethodGet, err: fmt.Errorf("GET %s: %w", u, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return "", &APIError{Method: http.MethodGet, URL: u, Status: resp.StatusCode, Message: message(data)}
	}
	h := sha256.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return "", &transportError{method: http.MethodGet, err: fmt.Errorf("GET %s: reading the file: %w", u, err)}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (p *Publisher) releaseURL(id int64) string {
	return p.repoURL(fmt.Sprintf("/releases/%d", id))
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
	// The tag is sent along although it does not change. An update of a draft that
	// leaves out tag_name was seen on 2026-09-13 to come back with the draft's tag
	// replaced by untagged-...; the draft has already been found under this tag, so
	// naming it cannot move somebody else's draft onto it.
	change := map[string]any{"body": notes.Body, "tag_name": rel.TagName}
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

	resp, err := p.client().Do(req)
	if err != nil {
		return nil, &transportError{method: method, err: fmt.Errorf("%s %s: %w", method, u, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &transportError{method: method, err: fmt.Errorf("%s %s: reading the answer: %w", method, u, err)}
	}
	if resp.StatusCode >= 300 {
		return nil, &APIError{Method: method, URL: u, Status: resp.StatusCode, Message: message(data)}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return nil, &transportError{method: method, err: fmt.Errorf("%s %s: unreadable answer: %w", method, u, err)}
		}
	}
	return resp.Header, nil
}

func (p *Publisher) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return http.DefaultClient
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
