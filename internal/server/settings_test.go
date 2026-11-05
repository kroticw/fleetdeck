package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestTheSettingsAreReadAndWritten(t *testing.T) {
	d, _ := testDeps()
	var wrote map[string]any
	d.Settings = func(context.Context) (any, error) {
		return map[string]any{"values": map[string]any{"ui.theme": "dark"}}, nil
	}
	d.WriteSettings = func(_ context.Context, v map[string]any) (any, error) {
		wrote = v
		if v["workers.effort"] == "huge" {
			return nil, fmt.Errorf("%w: workers.effort", ErrBadSettings)
		}
		return map[string]any{"values": v}, nil
	}
	if rec := do(d, http.MethodGet, "/api/settings", ""); rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(d, http.MethodPatch, "/api/settings", `{"workers.effort":"high"}`); rec.Code != http.StatusOK || wrote["workers.effort"] != "high" {
		t.Fatalf("PATCH: %d %s %v", rec.Code, rec.Body.String(), wrote)
	}
	if rec := do(d, http.MethodPatch, "/api/settings", `{"workers.effort":"huge"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a refused value: %d %s", rec.Code, rec.Body.String())
	}
	d.WriteSettings = func(context.Context, map[string]any) (any, error) { return nil, errors.New("disk full") }
	if rec := do(d, http.MethodPatch, "/api/settings", `{"ui.theme":"dark"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a failed write: %d", rec.Code)
	}
	d.Settings, d.WriteSettings = nil, nil
	if rec := do(d, http.MethodGet, "/api/settings", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no store: %d", rec.Code)
	}
}
