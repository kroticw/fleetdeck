package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
	"github.com/kroticw/fleetdeck/internal/review"
)

const reviewTimeout = 2 * time.Minute

// deliveryRetries bounds how many times recordDelivery retries against a
// fresh revision after ErrStale.
const deliveryRetries = 3

// maxReviewLines bounds one GET /api/review/lines answer: expanding the
// context is a page of lines at a time, and a file of a million lines must not
// come back whole to a single press.
const maxReviewLines = 500

// commitRe is what a commit looks like: a short or full hex object id. A
// comment's commit reaches git as a revision (internal/review.Lines), and
// git itself now refuses anything shaped like an option
// (--end-of-options) — this is the same rule enforced before the request
// ever reaches git, so a malformed commit is refused at the route rather
// than surfacing as an opaque git failure.
var commitRe = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// cardIDRe is the board's own card id shape (see internal/board's
// new_card.py). review.Dir joins the id onto the board directory with no
// cleaning of its own, so an id outside this shape — in particular one
// carrying ".." — is refused here, before it is ever joined onto a path.
var cardIDRe = regexp.MustCompile(`^T-\d+$`)

// reviewTarget is everything a review route needs about the card it names:
// the card itself, its review directory on the board, and the working tree
// its session reads from. ok false means the answer has been written.
type reviewTarget struct {
	card board.Card
	dir  string
	git  review.Git
}

func (d Deps) reviewTarget(w http.ResponseWriter, r *http.Request, cardPath string, needTree bool) (reviewTarget, bool) {
	if d.BoardDir == "" {
		unavailable(w, "a board directory")
		return reviewTarget{}, false
	}
	path, err := confineToBoard(d.BoardDir, cardPath)
	switch {
	case errors.Is(err, errOutsideBoard):
		fail(w, http.StatusForbidden, "reviews are read only for cards on this fleet's board")
		return reviewTarget{}, false
	case err != nil:
		unavailable(w, "a readable board directory")
		return reviewTarget{}, false
	}
	card, err := board.ParseCard(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fail(w, http.StatusNotFound, "there is no such card on this board")
		return reviewTarget{}, false
	case err != nil:
		fail(w, http.StatusUnprocessableEntity, "the card cannot be read: "+err.Error())
		return reviewTarget{}, false
	case card.ParseError != "":
		fail(w, http.StatusUnprocessableEntity, "the card cannot be read: "+card.ParseError)
		return reviewTarget{}, false
	}
	if card.ID == "" {
		fail(w, http.StatusUnprocessableEntity, "the card has no number, and a review is kept under the card's number")
		return reviewTarget{}, false
	}
	if !cardIDRe.MatchString(card.ID) {
		fail(w, http.StatusUnprocessableEntity, "the card's number is malformed, and a review is kept under the card's number")
		return reviewTarget{}, false
	}
	t := reviewTarget{card: card, dir: review.Dir(d.BoardDir, card.ID)}
	if !needTree {
		return t, true
	}
	if card.Session == "" {
		fail(w, http.StatusUnprocessableEntity, "nobody is keeping this card, so there is no branch to review")
		return reviewTarget{}, false
	}
	if d.ReviewWorkdir == nil {
		unavailable(w, "a way to find a session's working tree")
		return reviewTarget{}, false
	}
	dir, err := d.ReviewWorkdir(r.Context(), card)
	if err != nil {
		fail(w, http.StatusFailedDependency, err.Error())
		return reviewTarget{}, false
	}
	t.git = review.Git{Dir: dir}
	return t, true
}

// answerReviewWrite maps what review.Update can return onto a status.
func answerReviewWrite(w http.ResponseWriter, c review.Comments, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, c)
	case errors.Is(err, review.ErrStale):
		fail(w, http.StatusConflict, err.Error())
	case errors.Is(err, review.ErrNoComment):
		fail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, review.ErrNotDraft), errors.Is(err, review.ErrNoDrafts):
		fail(w, http.StatusBadRequest, err.Error())
	default:
		fail(w, http.StatusInternalServerError, err.Error())
	}
}

func (d Deps) handleReview(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	t, ok := d.reviewTarget(w, r, r.URL.Query().Get("card"), true)
	if !ok {
		return
	}
	v, err := review.Build(r.Context(), t.git, t.dir)
	if err != nil {
		// Every failure here is the working tree's, not the request's: not a
		// repository, no default branch, git timing out. Its words say which.
		fail(w, http.StatusFailedDependency, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleReviewLines answers lines from..to of path at commit, for the page to
// expand the unchanged context around a hunk. Clamped to the file and to
// maxReviewLines; total is the file's length, so the page knows when nothing
// is left below.
func (d Deps) handleReviewLines(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	commit, path := q.Get("commit"), q.Get("path")
	from, errFrom := strconv.Atoi(q.Get("from"))
	to, errTo := strconv.Atoi(q.Get("to"))
	if !commitRe.MatchString(commit) || !treePath(path) || errFrom != nil || errTo != nil || from < 1 || to < from {
		fail(w, http.StatusBadRequest, "lines need a commit, a relative path inside the tree and a line range from 1")
		return
	}
	t, ok := d.reviewTarget(w, r, q.Get("card"), true)
	if !ok {
		return
	}
	lines, err := t.git.Lines(r.Context(), commit, path)
	if err != nil {
		fail(w, linesFailure(err), err.Error())
		return
	}
	to = min(to, len(lines), from+maxReviewLines-1)
	out := []string{}
	if from <= to {
		out = lines[from-1 : to]
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "total": len(lines), "lines": out})
}

// linesFailure is the status a failed read of lines answers: a git that ran
// out of time is the working tree's trouble, anything else a file that is not
// there at that commit.
func linesFailure(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return http.StatusNotFound
}

// treePath reports whether path names a file inside the tree the way a diff
// does: relative, no "." or ".." segment. git's refusal of anything else
// says whether the path exists on disk, which the page must not learn.
func treePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func (d Deps) handleReviewAdd(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	var body struct {
		Card    string `json:"card"`
		Rev     int    `json:"rev"`
		Commit  string `json:"commit"`
		Path    string `json:"path"`
		Side    string `json:"side"`
		Start   int    `json:"start"`
		End     int    `json:"end"`
		Body    string `json:"body"`
		ReplyTo string `json:"replyTo"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	side := review.Side(body.Side)
	if (side != review.SideNew && side != review.SideOld) || !commitRe.MatchString(body.Commit) || body.Path == "" || body.Body == "" {
		fail(w, http.StatusBadRequest, "a comment needs a commit, a path, a side (new or old), a line range and a text")
		return
	}
	t, ok := d.reviewTarget(w, r, body.Card, true)
	if !ok {
		return
	}
	a, err := review.NewAnchor(r.Context(), t.git, body.Commit, body.Path, side, body.Start, body.End)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().Format(time.RFC3339)
	c, err := review.Update(t.dir, t.card.ID, body.Rev, func(c *review.Comments) error {
		c.AddDraft(a, body.Body, body.ReplyTo, now)
		return nil
	})
	answerReviewWrite(w, c, err)
}

func (d Deps) handleReviewEdit(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	var body struct {
		Card string `json:"card"`
		Rev  int    `json:"rev"`
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, ok := d.reviewTarget(w, r, body.Card, false)
	if !ok {
		return
	}
	c, err := review.Update(t.dir, t.card.ID, body.Rev, func(c *review.Comments) error { return c.EditDraft(body.ID, body.Body) })
	answerReviewWrite(w, c, err)
}

func (d Deps) handleReviewDelete(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	rev, err := strconv.Atoi(q.Get("rev"))
	if err != nil {
		fail(w, http.StatusBadRequest, "rev is required: a delete must name the revision it was made against")
		return
	}
	t, ok := d.reviewTarget(w, r, q.Get("card"), false)
	if !ok {
		return
	}
	c, err := review.Update(t.dir, t.card.ID, rev, func(c *review.Comments) error { return c.DeleteDraft(q.Get("id")) })
	answerReviewWrite(w, c, err)
}

func (d Deps) handleReviewResolve(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	var body struct {
		Card     string `json:"card"`
		Rev      int    `json:"rev"`
		ID       string `json:"id"`
		Resolved bool   `json:"resolved"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, ok := d.reviewTarget(w, r, body.Card, false)
	if !ok {
		return
	}
	c, err := review.Update(t.dir, t.card.ID, body.Rev, func(c *review.Comments) error { return c.SetResolved(body.ID, body.Resolved) })
	answerReviewWrite(w, c, err)
}

// handleReviewSend freezes the drafts into a round and tells the session.
// The round is frozen whether or not the session is reached, and the send is
// never repeated here: a reply always submits its text, so a second one is a
// second message to the agent, which only the operator may decide to send.
func (d Deps) handleReviewSend(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	// Lang is the page's language: the session is told about the round in it.
	var body struct {
		Card string `json:"card"`
		Rev  int    `json:"rev"`
		Lang string `json:"lang"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, ok := d.reviewTarget(w, r, body.Card, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), reviewTimeout)
	defer cancel()
	head, err := t.git.Head(ctx)
	if err != nil {
		fail(w, http.StatusFailedDependency, err.Error())
		return
	}
	base, err := t.git.Base(ctx)
	if err != nil {
		fail(w, http.StatusFailedDependency, err.Error())
		return
	}
	var n int
	c, err := review.Update(t.dir, t.card.ID, body.Rev, func(c *review.Comments) error {
		var ferr error
		n, ferr = c.Freeze(head, time.Now().Format(time.RFC3339), review.Place(ctx, t.git, base, head, c.Drafts()))
		return ferr
	})
	if err != nil {
		answerReviewWrite(w, c, err)
		return
	}
	delivery := d.notifyRound(ctx, t, n, body.Lang)
	if delivery != "" {
		c = recordDelivery(t, c, n, delivery)
	}
	writeJSON(w, http.StatusOK, map[string]any{"round": n, "delivery": delivery, "comments": c})
}

// handleReviewNotify tells the session about an existing round once more, on
// the operator's press and only then: one request, one message. It reads the
// card and the board, never the working tree.
func (d Deps) handleReviewNotify(w http.ResponseWriter, r *http.Request) {
	d, ok := d.forFleet(w, r)
	if !ok {
		return
	}
	// Rev is the page's revision, sent as with every review write. It is not
	// checked: a resend changes no comment, so a page one write behind may
	// still ask for it, and the delivery is recorded at whatever revision
	// the file is at.
	var body struct {
		Card  string `json:"card"`
		Rev   int    `json:"rev"`
		Round int    `json:"round"`
		Lang  string `json:"lang"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	t, ok := d.reviewTarget(w, r, body.Card, false)
	if !ok {
		return
	}
	c, err := review.Load(t.dir)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	i := slices.IndexFunc(c.Rounds, func(rd review.Round) bool { return rd.N == body.Round })
	if i < 0 {
		fail(w, http.StatusNotFound, "this review has no round "+strconv.Itoa(body.Round))
		return
	}
	if t.card.Session == "" {
		fail(w, http.StatusUnprocessableEntity, "nobody is keeping this card, so there is no session to tell")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), reviewTimeout)
	defer cancel()
	delivery := d.notifyRound(ctx, t, body.Round, body.Lang)
	if delivery != c.Rounds[i].Delivery {
		c = recordDelivery(t, c, body.Round, delivery)
	}
	writeJSON(w, http.StatusOK, map[string]any{"round": body.Round, "delivery": delivery, "comments": c})
}

// notifyRound sends the session the message pointing it at round n, in lang,
// once, and returns what went wrong, empty when it was delivered.
func (d Deps) notifyRound(ctx context.Context, t reviewTarget, n int, lang string) string {
	if d.SendToSession == nil {
		return "this panel has no way to reach the session"
	}
	text := orchestrator.ReviewRound(lang, n, filepath.Join(t.dir, "comments.json"), filepath.Join(t.dir, "replies.md"))
	if err := d.SendToSession(ctx, t.card.Session, text); err != nil {
		return err.Error()
	}
	return ""
}

// recordDelivery writes delivery onto round n, starting at c's revision.
// Nothing but a send or a resend writes delivery, so a stale write is one of
// those racing an unrelated write and is safe to retry at the fresh revision
// rather than drop. Bounded: a card under constant concurrent writes must not
// hang the request, and its answer carries delivery whether or not the write
// ever lands. The result is the file as last written, or c.
func recordDelivery(t reviewTarget, c review.Comments, n int, delivery string) review.Comments {
	rev := c.Rev
	for range deliveryRetries {
		updated, err := review.Update(t.dir, t.card.ID, rev, func(c *review.Comments) error { c.SetDelivery(n, delivery); return nil })
		if err == nil {
			return updated
		}
		if !errors.Is(err, review.ErrStale) {
			return c
		}
		fresh, err := review.Load(t.dir)
		if err != nil {
			return c
		}
		rev = fresh.Rev
	}
	return c
}
