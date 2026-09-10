package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleInput = `{"session_id":"abc-123","model":{"display_name":"Opus 5"},"cost":{"total_cost_usd":1.234},"context_window":{"used_percentage":41.7}}`

func TestRenderShowsModelCostAndContext(t *testing.T) {
	in, err := parse(strings.NewReader(sampleInput))
	if err != nil {
		t.Fatal(err)
	}
	got := render(in)
	for _, want := range []string{"Opus 5", "1.23", "42%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line missing %q: %s", want, got)
		}
	}
}

func TestReportPostsToThePanel(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		body = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	in, _ := parse(strings.NewReader(sampleInput))
	if err := report(srv.URL, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "abc-123") || !strings.Contains(body, "41.7") {
		t.Fatalf("report body lost data: %s", body)
	}
}

func TestReportFailureDoesNotBreakRendering(t *testing.T) {
	in, _ := parse(strings.NewReader(sampleInput))
	if err := report("http://127.0.0.1:1", in); err == nil {
		t.Fatal("an unreachable panel must be reported to the caller")
	}
	if got := render(in); got == "" {
		t.Fatal("the status line must still render when the panel is down")
	}
}

func TestEmptyStdinIsNotSuccess(t *testing.T) {
	if _, err := parse(strings.NewReader("")); err == nil {
		t.Fatal("empty input must fail: nothing to read is not a healthy status line")
	}
}
