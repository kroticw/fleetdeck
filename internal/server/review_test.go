package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/review"
)

// reviewDeps is a panel with a board holding one card kept by session
// abc12345, whose working tree is a repository with a branch one commit off
// origin/master.
func reviewDeps(t *testing.T) (Deps, *[]string, string, string) {
	t.Helper()
	d, calls := testDeps()
	boardDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(boardDir, "cards"), 0o700); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(boardDir, "cards", "T-057-x.md")
	body := "---\nid: T-057\nzone: planned\nstage: review\nprogress: 80\nsession: abc12345\ncreated: 2026-09-28\n---\n\n# A card\n"
	if err := os.WriteFile(card, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tree := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = tree
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet", "--initial-branch=master")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "T")
	git("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(tree, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "--quiet", "--message", "base")
	git("update-ref", "refs/remotes/origin/master", git("rev-parse", "HEAD"))
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")
	git("switch", "--quiet", "--create", "work")
	if err := os.WriteFile(filepath.Join(tree, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("commit", "--quiet", "--all", "--message", "work")
	head := git("rev-parse", "HEAD")

	d.BoardDir = boardDir
	d.ReviewWorkdir = func(_ context.Context, c board.Card) (string, error) {
		*calls = append(*calls, "workdir:"+c.Session)
		return tree, nil
	}
	d.SendToSession = func(_ context.Context, short, text string) error {
		*calls = append(*calls, "send:"+short+":"+text)
		return nil
	}
	return d, calls, card, head
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, body)
	}
	return v
}

func TestTheReviewOfACardIsItsSessionsBranch(t *testing.T) {
	t.Parallel()
	d, _, card, head := reviewDeps(t)
	rec := do(d, http.MethodGet, "/api/review?card="+card, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	v := decode[review.View](t, rec.Body.String())
	if v.Head != head || len(v.Files) != 1 || v.Files[0].NewPath != "a.go" {
		t.Fatalf("view = %+v", v)
	}
}

func TestACommentIsAnchoredToTheCommitThePageRead(t *testing.T) {
	t.Parallel()
	d, _, card, head := reviewDeps(t)
	body := `{"card":"` + card + `","rev":0,"commit":"` + head + `","path":"a.go","side":"new","start":3,"end":3,"body":"name it"}`
	rec := do(d, http.MethodPost, "/api/review/comments", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	c := decode[review.Comments](t, rec.Body.String())
	if len(c.Comments) != 1 || c.Comments[0].Anchor.Commit != head || c.Comments[0].Anchor.Text[0] != "func A() {}" {
		t.Fatalf("comments = %+v", c)
	}
}

func TestASecondTabWritingAtAnOldRevisionIsTold(t *testing.T) {
	t.Parallel()
	d, _, card, head := reviewDeps(t)
	body := `{"card":"` + card + `","rev":0,"commit":"` + head + `","path":"a.go","side":"new","start":1,"end":1,"body":"x"}`
	do(d, http.MethodPost, "/api/review/comments", body)
	if rec := do(d, http.MethodPost, "/api/review/comments", body); rec.Code != http.StatusConflict {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestSendingARoundFreezesTheDraftsAndTellsTheSessionOnce(t *testing.T) {
	t.Parallel()
	d, calls, card, head := reviewDeps(t)
	do(d, http.MethodPost, "/api/review/comments", `{"card":"`+card+`","rev":0,"commit":"`+head+`","path":"a.go","side":"new","start":3,"end":3,"body":"x"}`)
	rec := do(d, http.MethodPost, "/api/review/send", `{"card":"`+card+`","rev":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	sends := 0
	for _, c := range *calls {
		if strings.HasPrefix(c, "send:abc12345:") && strings.Contains(c, "round 1") && strings.Contains(c, filepath.Join("reviews", "T-057", "comments.json")) {
			sends++
		}
	}
	if sends != 1 {
		t.Fatalf("calls = %v", *calls)
	}
	c, err := review.Load(review.Dir(d.BoardDir, "T-057"))
	if err != nil || c.Comments[0].Round != 1 || c.Rounds[0].Head != head || c.Rounds[0].Positions["c1"].State != review.StateInPlace {
		t.Fatalf("file = %+v, %v", c, err)
	}
}

func TestAFailedDeliveryKeepsTheRoundAndSaysWhy(t *testing.T) {
	t.Parallel()
	d, _, card, head := reviewDeps(t)
	d.SendToSession = func(context.Context, string, string) error { return errors.New("daemon gone") }
	do(d, http.MethodPost, "/api/review/comments", `{"card":"`+card+`","rev":0,"commit":"`+head+`","path":"a.go","side":"new","start":3,"end":3,"body":"x"}`)
	rec := do(d, http.MethodPost, "/api/review/send", `{"card":"`+card+`","rev":1}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "daemon gone") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	c, _ := review.Load(review.Dir(d.BoardDir, "T-057"))
	if c.Comments[0].Round != 1 || !strings.Contains(c.Rounds[0].Delivery, "daemon gone") {
		t.Fatalf("file = %+v", c)
	}
}

func TestAReviewPathOutsideTheBoardIsRefused(t *testing.T) {
	t.Parallel()
	d, _, _, _ := reviewDeps(t)
	if rec := do(d, http.MethodGet, "/api/review?card=/etc/hosts", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestACardWithNoNumberHasNoReview(t *testing.T) {
	t.Parallel()
	d, _, card, _ := reviewDeps(t)
	if err := os.WriteFile(card, []byte("---\nzone: planned\nstage: review\nprogress: 80\nsession: abc12345\n---\n\n# A card\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := do(d, http.MethodGet, "/api/review?card="+card, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestASessionWithNoWorkingTreeIsNamedAsTheReason(t *testing.T) {
	t.Parallel()
	d, _, card, _ := reviewDeps(t)
	d.ReviewWorkdir = func(context.Context, board.Card) (string, error) {
		return "", errors.New("the session has not said where it works: its card has no worktree")
	}
	rec := do(d, http.MethodGet, "/api/review?card="+card, "")
	if rec.Code != http.StatusFailedDependency || !strings.Contains(rec.Body.String(), "worktree") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// A comment's commit is read straight into git show/diff (internal/review),
// where an option-shaped value is now refused by --end-of-options. This is
// the belt beside that suspenders: a request cannot even reach git with one,
// because the route itself only accepts what a commit actually looks like.
func TestACommitShapedLikeAnOptionIsRefused(t *testing.T) {
	t.Parallel()
	d, _, card, _ := reviewDeps(t)
	scratch := t.TempDir()
	out := filepath.Join(scratch, "x")
	body := `{"card":"` + card + `","rev":0,"commit":"--output=` + out + `","path":"a.go","side":"new","start":1,"end":1,"body":"x"}`
	rec := do(d, http.MethodPost, "/api/review/comments", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("git must not have been run with the commit as an option: %s exists", out)
	}
}

// review.Dir joins the card's own id onto the board directory with no
// cleaning of its own; a card whose id is not the board's own "T-NNN" shape
// must be refused before that join ever runs, or ".." in the id walks the
// review directory out of the board.
func TestACardIDOutsideTheBoardIsRefused(t *testing.T) {
	t.Parallel()
	d, _, card, _ := reviewDeps(t)
	bad := "---\nid: ../../x\nzone: planned\nstage: review\nprogress: 80\nsession: abc12345\n---\n\n# A card\n"
	if err := os.WriteFile(card, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := do(d, http.MethodGet, "/api/review?card="+card, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	escaped := filepath.Join(filepath.Dir(d.BoardDir), "x")
	if _, err := os.Stat(escaped); !os.IsNotExist(err) {
		t.Fatalf("a review directory was created outside the board: %s", escaped)
	}
}

// SetDelivery is recorded against the revision the freeze just wrote; if
// another write lands on comments.json in between — here, SendToSession
// itself makes one, standing in for a second tab — that write goes stale and
// must be retried at the fresh revision rather than silently dropped, or the
// round reads as delivered with no way to see it failed.
func TestADeliveryFailureSurvivesAConcurrentWrite(t *testing.T) {
	t.Parallel()
	d, _, card, head := reviewDeps(t)
	do(d, http.MethodPost, "/api/review/comments", `{"card":"`+card+`","rev":0,"commit":"`+head+`","path":"a.go","side":"new","start":3,"end":3,"body":"x"}`)
	dir := review.Dir(d.BoardDir, "T-057")
	d.SendToSession = func(context.Context, string, string) error {
		cur, err := review.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := review.Update(dir, "T-057", cur.Rev, func(*review.Comments) error { return nil }); err != nil {
			t.Fatal(err)
		}
		return errors.New("daemon gone")
	}
	rec := do(d, http.MethodPost, "/api/review/send", `{"card":"`+card+`","rev":1}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "daemon gone") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	c, err := review.Load(dir)
	if err != nil || len(c.Rounds) != 1 || !strings.Contains(c.Rounds[0].Delivery, "daemon gone") {
		t.Fatalf("file = %+v, %v", c, err)
	}
}

// sentRound is reviewDeps with one comment sent as round 1, the send failing
// with "daemon gone" so the round carries a delivery error.
func sentRound(t *testing.T) (Deps, *[]string, string) {
	t.Helper()
	d, calls, card, head := reviewDeps(t)
	send := d.SendToSession
	d.SendToSession = func(context.Context, string, string) error { return errors.New("daemon gone") }
	do(d, http.MethodPost, "/api/review/comments", `{"card":"`+card+`","rev":0,"commit":"`+head+`","path":"a.go","side":"new","start":3,"end":3,"body":"x"}`)
	if rec := do(d, http.MethodPost, "/api/review/send", `{"card":"`+card+`","rev":1}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	d.SendToSession = send
	*calls = nil
	return d, calls, card
}

func TestResendingARoundTellsTheSessionOnceAndClearsTheDelivery(t *testing.T) {
	t.Parallel()
	d, calls, card := sentRound(t)
	// The resend reads nothing from the working tree.
	d.ReviewWorkdir = nil
	rec := do(d, http.MethodPost, "/api/review/notify", `{"card":"`+card+`","rev":3,"round":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "send:abc12345:") || !strings.Contains((*calls)[0], "round 1") {
		t.Fatalf("calls = %v", *calls)
	}
	c, err := review.Load(review.Dir(d.BoardDir, "T-057"))
	if err != nil || c.Rounds[0].Delivery != "" {
		t.Fatalf("file = %+v, %v", c, err)
	}
}

func TestAFailedResendIsRecordedOnTheRound(t *testing.T) {
	t.Parallel()
	d, _, card := sentRound(t)
	d.SendToSession = func(context.Context, string, string) error { return errors.New("still gone") }
	rec := do(d, http.MethodPost, "/api/review/notify", `{"card":"`+card+`","rev":3,"round":1}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "still gone") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	c, _ := review.Load(review.Dir(d.BoardDir, "T-057"))
	if c.Rounds[0].Delivery != "still gone" {
		t.Fatalf("file = %+v", c)
	}
}

func TestResendingAnUnknownRoundIsNotFound(t *testing.T) {
	t.Parallel()
	d, calls, card := sentRound(t)
	if rec := do(d, http.MethodPost, "/api/review/notify", `{"card":"`+card+`","rev":3,"round":2}`); rec.Code != http.StatusNotFound {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %v", *calls)
	}
}

func TestResendingForACardOutsideTheBoardIsRefused(t *testing.T) {
	t.Parallel()
	d, calls, _ := sentRound(t)
	if rec := do(d, http.MethodPost, "/api/review/notify", `{"card":"/etc/hosts","rev":3,"round":1}`); rec.Code != http.StatusForbidden {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %v", *calls)
	}
}

// draft is reviewDeps with one draft comment c1 at revision 1.
func draft(t *testing.T) (Deps, string) {
	t.Helper()
	d, _, card, head := reviewDeps(t)
	if rec := do(d, http.MethodPost, "/api/review/comments", `{"card":"`+card+`","rev":0,"commit":"`+head+`","path":"a.go","side":"new","start":3,"end":3,"body":"x"}`); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	return d, card
}

func TestADraftIsEdited(t *testing.T) {
	t.Parallel()
	d, card := draft(t)
	rec := do(d, http.MethodPatch, "/api/review/comments", `{"card":"`+card+`","rev":1,"id":"c1","body":"y"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if c := decode[review.Comments](t, rec.Body.String()); c.Comments[0].Body != "y" || c.Rev != 2 {
		t.Fatalf("comments = %+v", c)
	}
}

func TestASentCommentCannotBeEdited(t *testing.T) {
	t.Parallel()
	d, card := draft(t)
	do(d, http.MethodPost, "/api/review/send", `{"card":"`+card+`","rev":1}`)
	if rec := do(d, http.MethodPatch, "/api/review/comments", `{"card":"`+card+`","rev":2,"id":"c1","body":"y"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestADraftIsDeleted(t *testing.T) {
	t.Parallel()
	d, card := draft(t)
	rec := do(d, http.MethodDelete, "/api/review/comments?card="+card+"&rev=1&id=c1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if c := decode[review.Comments](t, rec.Body.String()); len(c.Comments) != 0 {
		t.Fatalf("comments = %+v", c)
	}
}

func TestADeleteWithoutARevisionIsRefused(t *testing.T) {
	t.Parallel()
	d, card := draft(t)
	if rec := do(d, http.MethodDelete, "/api/review/comments?card="+card+"&id=c1", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if c, _ := review.Load(review.Dir(d.BoardDir, "T-057")); len(c.Comments) != 1 {
		t.Fatalf("file = %+v", c)
	}
}

func TestACommentIsResolvedAndReopened(t *testing.T) {
	t.Parallel()
	d, card := draft(t)
	for rev, resolved := range []bool{true, false} {
		body := `{"card":"` + card + `","rev":` + strconv.Itoa(rev+1) + `,"id":"c1","resolved":` + strconv.FormatBool(resolved) + `}`
		rec := do(d, http.MethodPost, "/api/review/resolve", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if c := decode[review.Comments](t, rec.Body.String()); c.Comments[0].Resolved != resolved {
			t.Fatalf("resolved %v: comments = %+v", resolved, c)
		}
	}
}

func TestAMissingCardIsNotFound(t *testing.T) {
	t.Parallel()
	d, _, card, _ := reviewDeps(t)
	if rec := do(d, http.MethodGet, "/api/review?card="+strings.TrimSuffix(card, ".md")+"-gone.md", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAnUnreadableCardSaysWhy(t *testing.T) {
	t.Parallel()
	d, _, card, _ := reviewDeps(t)
	if err := os.WriteFile(card, []byte("no frontmatter here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := do(d, http.MethodGet, "/api/review?card="+card, "")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "no frontmatter block") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	dir := filepath.Join(d.BoardDir, "cards", "T-058-dir.md")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec = do(d, http.MethodGet, "/api/review?card="+dir, "")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "read card") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

// A review request goes to the board and the working tree of the fleet the
// tab names, and a card of one fleet is not reviewed through another.
func TestAReviewGoesThroughTheTabsFleet(t *testing.T) {
	t.Parallel()
	d, calls, dirs := twoFleetWrites(t)
	tree, _, _, head := reviewDeps(t)
	card := filepath.Join(dirs[2], "cards", "T-057-x.md")
	if err := os.WriteFile(card, []byte("---\nid: T-057\nzone: planned\nstage: review\nprogress: 80\nsession: abc12345\n---\n\n# B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fleets := d.Fleet
	d.Fleet = func(name string) (FleetDeps, error) {
		f, err := fleets(name)
		if name == "B" {
			f.ReviewWorkdir = func(ctx context.Context, c board.Card) (string, error) {
				*calls = append(*calls, "workdir:B:"+c.Session)
				return tree.ReviewWorkdir(ctx, c)
			}
		}
		return f, err
	}
	rec := do(d, http.MethodGet, "/api/review?fleet=B&card="+card, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if v := decode[review.View](t, rec.Body.String()); v.Head != head {
		t.Fatalf("view = %+v", v)
	}
	if !slices.Contains(*calls, "workdir:B:abc12345") {
		t.Fatalf("calls = %v", *calls)
	}
	if rec := do(d, http.MethodGet, "/api/review?fleet=A&card="+card, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("fleet A: %d %s", rec.Code, rec.Body.String())
	}
}
