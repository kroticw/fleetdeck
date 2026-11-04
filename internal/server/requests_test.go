package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/kroticw/fleetdeck/internal/codexruntime"
	"github.com/kroticw/fleetdeck/internal/executor"
)

func TestSessionRequestsAreListedAndAnsweredExplicitly(t *testing.T) {
	d, _ := testDeps()
	var answered []codexruntime.Answer
	d.SessionRequests = func(_ context.Context, id string) ([]codexruntime.PendingRequest, error) {
		if id != "codex:x" {
			return nil, executor.ErrUnsupported
		}
		return []codexruntime.PendingRequest{{Token: "tok", Kind: "command", Command: "ls"}}, nil
	}
	d.RespondRequest = func(_ context.Context, _ string, a codexruntime.Answer) error {
		answered = append(answered, a)
		if a.Token == "stale" {
			return executor.ErrConflict
		}
		if a.Token == "lost" {
			return codexruntime.ErrReplyUnknown
		}
		return nil
	}
	rec := do(d, http.MethodGet, "/api/sessions/codex:x/requests", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"command":"ls"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(d, http.MethodGet, "/api/sessions/abc12345/requests", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("claude session: %d", rec.Code)
	}
	if rec := do(d, http.MethodPost, "/api/sessions/codex:x/requests/respond", `{"token":"tok","decision":"accept"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(d, http.MethodPost, "/api/sessions/codex:x/requests/respond", `{"token":"stale","decision":"accept"}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale: %d", rec.Code)
	}
	rec = do(d, http.MethodPost, "/api/sessions/codex:x/requests/respond", `{"token":"lost","decision":"accept"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "do not answer again") {
		t.Fatalf("lost: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(d, http.MethodPost, "/api/sessions/codex:x/requests/respond", `{"token":"tok","rpc":"raw"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown fields must be refused: %d", rec.Code)
	}
	if len(answered) != 3 || answered[0].Decision != "accept" {
		t.Fatalf("answers %+v", answered)
	}
}
