package supervisor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPanelIsToldItIsHandingOverBeforeItIsStopped(t *testing.T) {
	told := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		told <- r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	announceHandover(context.Background(), srv.URL+"/")
	select {
	case got := <-told:
		if got != "POST /api/handover" {
			t.Fatalf("told %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the panel was not told")
	}
	// A panel that does not know the route, or none at all, is no error.
	announceHandover(context.Background(), "http://127.0.0.1:1/")
}
