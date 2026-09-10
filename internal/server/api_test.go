package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/board"
	"github.com/kroticw/fleetdeck/internal/daemon"
	"github.com/kroticw/fleetdeck/internal/state"
	"github.com/kroticw/fleetdeck/internal/transcript"
)

// testDeps returns a Deps whose every function is wired to a recorder, plus the
// slice those functions append to. A test that wants a failure replaces exactly
// one function; a test that wants a missing dependency sets one to nil.
func testDeps() (Deps, *[]string) {
	var calls []string
	return Deps{
		Snapshot: func() state.Snapshot {
			return state.Snapshot{Cards: []board.Card{{Path: "/b/c.md", Stage: "active"}}}
		},
		SendText: func(session, text string) error {
			calls = append(calls, "text:"+session+":"+text)
			return nil
		},
		SendKeys: func(session, keys string) error {
			calls = append(calls, "keys:"+session+":"+keys)
			return nil
		},
		ReadScreen: func(session string, tail int) daemon.ScreenResult {
			calls = append(calls, fmt.Sprintf("screen:%s:%d", session, tail))
			return daemon.ScreenResult{Screen: "screen"}
		},
		SetCardField: func(path, field, value string) error {
			calls = append(calls, "card:"+path+":"+field+":"+value)
			return nil
		},
		PutStatus: func(sessionID, model string, contextPercent, costUSD float64) {
			calls = append(calls, fmt.Sprintf("status:%s:%s:%.1f:%.2f", sessionID, model, contextPercent, costUSD))
		},
		Digest: func(sessionID string, limit int) ([]transcript.Step, error) {
			calls = append(calls, fmt.Sprintf("digest:%s:%d", sessionID, limit))
			return []transcript.Step{{Role: "assistant", Text: "hi"}}, nil
		},
		SetOrchestratorSession: func(id string) error {
			calls = append(calls, "orchestrator:"+id)
			return nil
		},
	}, &calls
}

// do sends a request the way the panel's own page does: a JSON content type on
// anything carrying a body, and no Origin, which is what a non-browser client
// sends. Tests that care about either header build the request themselves with
// send below.
func do(d Deps, method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	return send(d, r)
}

func send(d Deps, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, r)
	return rec
}

func TestSnapshotIsServedAsJSON(t *testing.T) {
	d, _ := testDeps()
	rec := do(d, http.MethodGet, "/api/snapshot", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"/b/c.md"`) {
		t.Fatalf("snapshot body missing cards: %s", rec.Body.String())
	}
}

func TestSendTextReachesTheSession(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPost, "/api/sessions/abc123/text", `{"text":"hi","submit":true}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "text:abc123:hi" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

func TestSendTextAcceptsAnAbsentSubmitField(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPost, "/api/sessions/abc123/text", `{"text":"hi"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

// The daemon's reply operation always submits: there is no way to place text in a
// session's prompt without sending it. Accepting submit:false and ignoring it would
// be a promise the server cannot keep, so it is refused outright.
func TestSendTextRefusesAnExplicitSubmitFalse(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPost, "/api/sessions/abc123/text", `{"text":"hi","submit":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("submit:false must be refused with 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the daemon, got %v", *calls)
	}
	if !strings.Contains(rec.Body.String(), "submit") {
		t.Fatalf("the refusal must explain itself, got %s", rec.Body.String())
	}
}

func TestSendTextReportsADaemonFailure(t *testing.T) {
	d, _ := testDeps()
	d.SendText = func(string, string) error { return errors.New("daemon is down") }
	rec := do(d, http.MethodPost, "/api/sessions/abc/text", `{"text":"hi"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
}

func TestSendKeysReachesTheSession(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPost, "/api/sessions/abc123/keys", `{"keys":"\u001b"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "keys:abc123:\x1b" {
		t.Fatalf("unexpected calls: %q", *calls)
	}
}

func TestScreenIsServedWithTheRequestedTail(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodGet, "/api/sessions/abc/screen?tail=512", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "screen:abc:512" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
	if !strings.Contains(rec.Body.String(), `"screen"`) {
		t.Fatalf("screen body missing: %s", rec.Body.String())
	}
}

func TestScreenTailDefaultsToAWholeScreen(t *testing.T) {
	d, calls := testDeps()
	if rec := do(d, http.MethodGet, "/api/sessions/abc/screen", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if len(*calls) != 1 || (*calls)[0] != fmt.Sprintf("screen:abc:%d", defaultTailBytes) {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

func TestScreenTailIsCappedAtTheDocumentedMaximum(t *testing.T) {
	d, calls := testDeps()
	if rec := do(d, http.MethodGet, "/api/sessions/abc/screen?tail=99999999", ""); rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if len(*calls) != 1 || (*calls)[0] != fmt.Sprintf("screen:abc:%d", maxTailBytes) {
		t.Fatalf("tail must be capped, got %v", *calls)
	}
}

func TestScreenRefusesATailThatIsNotANumber(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodGet, "/api/sessions/abc/screen?tail=abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a non-numeric tail must be refused with 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the daemon, got %v", *calls)
	}
}

func TestScreenRefusesANegativeTail(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodGet, "/api/sessions/abc/screen?tail=-5", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a negative tail must be refused with 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the daemon, got %v", *calls)
	}
}

// ScreenResult bundles Screen and Err precisely so a failed read does not throw
// away the bytes it did collect (a kicked attach carries the prefix read before
// the eviction). The handler must pass that prefix on rather than drop it.
func TestScreenReportsAFailureWithoutDroppingWhatItRead(t *testing.T) {
	d, _ := testDeps()
	d.ReadScreen = func(string, int) daemon.ScreenResult {
		return daemon.ScreenResult{Screen: "partial output", Err: errors.New("kicked")}
	}
	rec := do(d, http.MethodGet, "/api/sessions/abc/screen", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "partial output") {
		t.Fatalf("the partial screen must survive the error: %s", rec.Body.String())
	}
}

// cardDeps wires testDeps to a real board directory holding one card, and
// returns the card's resolved path — resolved because the handler confines
// writes by resolving both sides, and on macOS t.TempDir() sits under /var,
// which is a symlink to /private/var.
func cardDeps(t *testing.T) (Deps, *[]string, string) {
	t.Helper()
	d, calls := testDeps()
	board := t.TempDir()
	card := filepath.Join(board, "c.md")
	if err := os.WriteFile(card, []byte("---\nstage: new\n---\n"), 0o600); err != nil {
		t.Fatalf("write card: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(card)
	if err != nil {
		t.Fatalf("resolve card: %v", err)
	}
	d.BoardDir = board
	return d, calls, resolved
}

func TestPatchCardWritesTheField(t *testing.T) {
	d, calls, card := cardDeps(t)
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"40"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "card:"+card+":progress:40" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

func TestPatchCardRefusesFieldsThePanelDoesNotOwn(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(string, string, string) error {
		return fmt.Errorf("%w: session", board.ErrUnknownField)
	}
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"session","value":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("writing a foreign field must be refused with 400, got %d", rec.Code)
	}
}

func TestPatchCardReportsAMissingCardAsNotFound(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(path, _, _ string) error {
		return fmt.Errorf("re-read card before write: %w", &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist})
	}
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"gone.md","field":"stage","value":"done"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a card that does not exist must be 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchCardReportsAWriteFailureAsServerError(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(path, _, _ string) error {
		return fmt.Errorf("rename temp file over card: %w", &os.LinkError{Op: "rename", Old: path, New: path, Err: fs.ErrPermission})
	}
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"done"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("an I/O failure must be 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// board.SetField refuses a value the board's own validator would reject before it
// touches the disk, and returns a plain error for it. That is the client's mistake,
// not the server's, so it must not be reported as a 500.
func TestPatchCardReportsARefusedValueAsBadRequest(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(string, string, string) error { return errors.New(`unknown stage "shipping"`) }
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"stage","value":"shipping"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a refused value must be 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// A card the board cannot write into is not the caller's mistake: the request
// was well formed and the card is not. Answering 400 tells the operator to fix a
// request that was fine.
//
// These run against the real board.SetField rather than a stub, so the messages
// the status mapping keys on come from board itself. A rewording there fails
// this test instead of quietly turning a 422 back into a 400.
func TestPatchCardReportsACardThatCannotAcceptTheWriteAsUnprocessable(t *testing.T) {
	cases := map[string]struct {
		card  string
		field string
		value string
		want  int
	}{
		"no frontmatter at all":      {"# just a heading\n", "progress", "40", http.StatusUnprocessableEntity},
		"frontmatter will not parse": {"---\nstage: [unclosed\n---\n", "progress", "40", http.StatusUnprocessableEntity},
		"no line for the field":      {"---\nstage: new\n---\n", "progress", "40", http.StatusUnprocessableEntity},
		// Still the caller's mistake: a value the board will never accept.
		"value off the ladder": {"---\nstage: new\nprogress: 0\n---\n", "progress", "37", http.StatusBadRequest},
		// And so is a field the panel does not own.
		"field the panel does not own": {"---\nstage: new\nprogress: 0\n---\n", "session", "abc", http.StatusBadRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d, _, card := cardDeps(t)
			if err := os.WriteFile(card, []byte(tc.card), 0o600); err != nil {
				t.Fatalf("write card: %v", err)
			}
			d.SetCardField = board.SetField
			body := `{"path":"c.md","field":"` + tc.field + `","value":"` + tc.value + `"}`
			if rec := do(d, http.MethodPatch, "/api/cards", body); rec.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

// The one refusal in this family that no card content can be built to provoke
// from outside: board refuses a substitution that would change the file's line
// count. It is still the card's fault, not the request's.
func TestPatchCardReportsALineCountRefusalAsUnprocessable(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(string, string, string) error {
		return errors.New("card c.md refusing to write: line count would change")
	}
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"40"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchCardRefusesAnEmptyPath(t *testing.T) {
	d, calls, _ := cardDeps(t)
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"","field":"stage","value":"done"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the board, got %v", *calls)
	}
}

// The four fields below are exactly what cmd/fleetdeck-status posts. Changing
// either side without the other silently breaks the only source of a session's
// model name, cost and context percentage.
func TestStatusReportIsAccepted(t *testing.T) {
	d, calls := testDeps()
	body := `{"sessionId":"abc-123","model":"Opus 4.1","costUSD":1.25,"contextPercent":42.5}`
	rec := do(d, http.MethodPost, "/api/status", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "status:abc-123:Opus 4.1:42.5:1.25" {
		t.Fatalf("the report did not arrive field for field: %v", *calls)
	}
}

func TestStatusReportWithoutASessionIDIsRefused(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPost, "/api/status", `{"sessionId":"","model":"Opus","costUSD":0,"contextPercent":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("an unattributable report must not be stored: %v", *calls)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	d, _ := testDeps()
	if rec := do(d, http.MethodGet, "/api/nothing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestWrongMethodIsRefused(t *testing.T) {
	d, _ := testDeps()
	if rec := do(d, http.MethodPost, "/api/snapshot", `{}`); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
}

func TestMalformedBodyIsRejected(t *testing.T) {
	d, _ := testDeps()
	if rec := do(d, http.MethodPost, "/api/sessions/abc/text", `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a truncated body must be refused, got %d", rec.Code)
	}
}

// A typo in a field name must fail loudly rather than be silently dropped —
// the same decision internal/config took with KnownFields(true).
func TestUnknownJSONFieldIsRejected(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPost, "/api/sessions/abc/text", `{"txet":"hi"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field must be refused with 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the daemon, got %v", *calls)
	}
}

func TestASecondJSONValueIsRejected(t *testing.T) {
	d, _ := testDeps()
	if rec := do(d, http.MethodPost, "/api/sessions/abc/text", `{"text":"hi"}{"text":"again"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	d, calls := testDeps()
	huge := `{"text":"` + strings.Repeat("x", maxBodyBytes+1) + `"}`
	rec := do(d, http.MethodPost, "/api/sessions/abc/text", huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized body must be refused with 413, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the daemon, got %v", *calls)
	}
}

// A nil Deps function is a configuration fact — a panel wired without that
// capability — and must answer 503, never panic.
func TestANilDependencyIsUnavailableNotAPanic(t *testing.T) {
	cases := []struct {
		name   string
		blank  func(*Deps)
		method string
		target string
		body   string
	}{
		{"snapshot", func(d *Deps) { d.Snapshot = nil }, http.MethodGet, "/api/snapshot", ""},
		{"text", func(d *Deps) { d.SendText = nil }, http.MethodPost, "/api/sessions/a/text", `{"text":"hi"}`},
		{"keys", func(d *Deps) { d.SendKeys = nil }, http.MethodPost, "/api/sessions/a/keys", `{"keys":"x"}`},
		{"screen", func(d *Deps) { d.ReadScreen = nil }, http.MethodGet, "/api/sessions/a/screen", ""},
		{"cards", func(d *Deps) { d.SetCardField = nil }, http.MethodPatch, "/api/cards", `{"path":"/b/c.md","field":"stage","value":"new"}`},
		{"status", func(d *Deps) { d.PutStatus = nil }, http.MethodPost, "/api/status", `{"sessionId":"a","model":"m","costUSD":0,"contextPercent":0}`},
		{"digest", func(d *Deps) { d.Digest = nil }, http.MethodGet, "/api/sessions/a/digest", ""},
		{"config", func(d *Deps) { d.SetOrchestratorSession = nil }, http.MethodPatch, "/api/config", `{"orchestratorSession":"abc"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _, _ := cardDeps(t)
			tc.blank(&d)
			rec := do(d, tc.method, tc.target, tc.body)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("a nil %s dependency must answer 503, got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// A card write is two steps — the file, then the commit — and only the first one
// decides whether the operator's edit took effect. An error that reaches the
// handler after the file was written must not be shown as a failed write: the
// operator would redo an edit that already happened, and doing that twice to a
// progress field moves it somewhere nobody asked for.
func TestPatchCardReportsAnUncommittedWriteAsSuccess(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(string, string, string) error {
		return fmt.Errorf("%w: git commit timed out after 30s, likely a signing passphrase prompt", ErrFieldWrittenNotCommitted)
	}
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"40"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a written but uncommitted field must be 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "signing passphrase prompt") {
		t.Fatalf("the reason must reach the operator verbatim: %s", body)
	}
	if !strings.Contains(body, `"committed":false`) {
		t.Fatalf("an uncommitted write must be distinguishable from an ordinary success: %s", body)
	}
}

// Nothing to commit means the field already held this value, so no history is
// missing and there is nothing to tell the operator about.
func TestPatchCardReportsNothingToCommitAsAnOrdinarySuccess(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(string, string, string) error {
		return fmt.Errorf("nothing to commit for c.md: %w", board.ErrNothingToCommit)
	}
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"40"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("nothing to commit must be an ordinary success, got %d: %s", rec.Code, rec.Body.String())
	}
}

// The same sentinel wrapped both ways round: a commit that found nothing to do is
// not a write missing from git history, whichever way the caller wrapped it.
func TestPatchCardTreatsNothingToCommitAsSuccessEvenWhenWrappedAsUncommitted(t *testing.T) {
	d, _, _ := cardDeps(t)
	d.SetCardField = func(string, string, string) error {
		return fmt.Errorf("%w: %w", ErrFieldWrittenNotCommitted, board.ErrNothingToCommit)
	}
	rec := do(d, http.MethodPatch, "/api/cards", `{"path":"c.md","field":"progress","value":"40"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDigestIsServed(t *testing.T) {
	d, _ := testDeps()
	d.Digest = func(_ string, limit int) ([]transcript.Step, error) {
		if limit != 7 {
			t.Errorf("limit must reach the source, got %d", limit)
		}
		return []transcript.Step{{Role: "assistant", Text: "hello"}}, nil
	}
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions/abc/digest?limit=7", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("digest not served: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDigestWithoutTranscriptIsNotAnEmptyList(t *testing.T) {
	d, _ := testDeps()
	d.Digest = func(string, int) ([]transcript.Step, error) { return nil, transcript.ErrNoTranscript }
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions/abc/digest", nil))
	if rec.Code == http.StatusOK {
		t.Fatal("a missing transcript must be an error, not an empty digest that looks like a quiet session")
	}
}

func TestDigestDefaultsTheLimit(t *testing.T) {
	d, _ := testDeps()
	d.Digest = func(_ string, limit int) ([]transcript.Step, error) {
		if limit != 20 {
			t.Errorf("want the default limit of 20, got %d", limit)
		}
		return []transcript.Step{{Role: "assistant", Text: "hello"}}, nil
	}
	rec := do(d, http.MethodGet, "/api/sessions/abc/digest", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestPatchConfigSetsTheOrchestratorSession(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPatch, "/api/config", `{"orchestratorSession":"abc123"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "orchestrator:abc123" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

func TestPatchConfigAcceptsAnEmptyStringToUnpin(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPatch, "/api/config", `{"orchestratorSession":""}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 1 || (*calls)[0] != "orchestrator:" {
		t.Fatalf("unexpected calls: %v", *calls)
	}
}

func TestPatchConfigRequiresTheOrchestratorSessionKey(t *testing.T) {
	d, calls := testDeps()
	rec := do(d, http.MethodPatch, "/api/config", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("an absent key must be refused with 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing must reach the store, got %v", *calls)
	}
	if !strings.Contains(rec.Body.String(), "orchestratorSession is required") {
		t.Fatalf("the refusal must explain itself, got %s", rec.Body.String())
	}
}

func TestPatchConfigReportsAStoreFailure(t *testing.T) {
	d, _ := testDeps()
	d.SetOrchestratorSession = func(string) error { return errors.New("could not save config") }
	rec := do(d, http.MethodPatch, "/api/config", `{"orchestratorSession":"abc"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
}
