package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

func serveDeps(d Deps, r *http.Request) (int, string) {
	rec := httptest.NewRecorder()
	New(d).ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

func TestOrchestratorPreviewIsServedInTheAskedLanguage(t *testing.T) {
	var asked string
	d := Deps{OrchestratorPreview: func(lang string) (orchestrator.Preview, error) {
		asked = lang
		return orchestrator.Preview{Path: "/w/docs/orchestrator.md", Message: "read it", CanStart: true}, nil
	}}
	code, body := serveDeps(d, httptest.NewRequest(http.MethodGet, "/api/orchestrator?lang=ru", nil))
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", code, body)
	}
	if asked != "ru" {
		t.Errorf("the preview was built in %q, want ru", asked)
	}
	var got orchestrator.Preview
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "/w/docs/orchestrator.md" || got.Message != "read it" || !got.CanStart {
		t.Errorf("preview = %+v", got)
	}
}

func TestOrchestratorRoutesWithoutTheirDependencyAnswer503(t *testing.T) {
	for _, r := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/orchestrator", nil),
		jsonRequest(http.MethodPost, "/api/orchestrator", `{"new":true}`),
	} {
		if code, body := serveDeps(Deps{}, r); code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: want 503, got %d: %s", r.Method, r.URL, code, body)
		}
	}
}

func TestOrchestratorAppointmentIsPassedOnAndItsStepsAnswered(t *testing.T) {
	var got orchestrator.Request
	d := Deps{Appoint: func(_ context.Context, req orchestrator.Request) (orchestrator.Result, error) {
		got = req
		return orchestrator.Result{
			Session: "22222222",
			Steps:   []orchestrator.Step{{Name: "brief", Note: "wrote /w/docs/orchestrator.md"}, {Name: "message", Error: "not taking input"}},
		}, nil
	}}
	code, body := serveDeps(d, jsonRequest(http.MethodPost, "/api/orchestrator", `{"session":"22222222","lang":"ru"}`))
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", code, body)
	}
	if got != (orchestrator.Request{Session: "22222222", Lang: "ru"}) {
		t.Errorf("request passed on as %+v", got)
	}
	var res orchestrator.Result
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Session != "22222222" || len(res.Steps) != 2 || res.Steps[1].Error != "not taking input" {
		t.Errorf("result = %+v", res)
	}
}

func TestOrchestratorRefusalsAreAnsweredByKind(t *testing.T) {
	for err, want := range map[error]int{
		orchestrator.ErrBadRequest:  http.StatusBadRequest,
		orchestrator.ErrBusy:        http.StatusConflict,
		orchestrator.ErrNoBoard:     http.StatusConflict,
		orchestrator.ErrCannotStart: http.StatusServiceUnavailable,
	} {
		d := Deps{Appoint: func(context.Context, orchestrator.Request) (orchestrator.Result, error) {
			return orchestrator.Result{}, err
		}}
		code, body := serveDeps(d, jsonRequest(http.MethodPost, "/api/orchestrator", `{"new":true}`))
		if code != want {
			t.Errorf("%v: want %d, got %d", err, want, code)
		}
		if !strings.Contains(body, err.Error()) {
			t.Errorf("%v: the answer does not say why: %s", err, body)
		}
	}
}

func TestOrchestratorAppointmentRefusesUnknownFields(t *testing.T) {
	d := Deps{Appoint: func(context.Context, orchestrator.Request) (orchestrator.Result, error) {
		t.Error("an unreadable request reached the appointment")
		return orchestrator.Result{}, nil
	}}
	if code, _ := serveDeps(d, jsonRequest(http.MethodPost, "/api/orchestrator", `{"sesion":"22222222"}`)); code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", code)
	}
}

// An appointment outlives the request that asked for it. A page closed while a
// new session is starting would otherwise leave a session started and never
// told why.
func TestOrchestratorAppointmentIsNotCancelledWithItsRequest(t *testing.T) {
	cancelled := make(chan bool, 1)
	d := Deps{Appoint: func(ctx context.Context, _ orchestrator.Request) (orchestrator.Result, error) {
		select {
		case <-ctx.Done():
			cancelled <- true
		case <-time.After(50 * time.Millisecond):
			cancelled <- false
		}
		return orchestrator.Result{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	r := jsonRequest(http.MethodPost, "/api/orchestrator", `{"new":true}`).WithContext(ctx)
	cancel()
	serveDeps(d, r)
	if <-cancelled {
		t.Error("the appointment was cancelled with its request")
	}
}

func TestOrchestratorPreviewFailureIsAnswered(t *testing.T) {
	d := Deps{OrchestratorPreview: func(string) (orchestrator.Preview, error) {
		return orchestrator.Preview{}, errors.New("no knowledge in this build")
	}}
	code, body := serveDeps(d, httptest.NewRequest(http.MethodGet, "/api/orchestrator", nil))
	if code != http.StatusInternalServerError || !strings.Contains(body, "no knowledge in this build") {
		t.Errorf("got %d %s", code, body)
	}
}
