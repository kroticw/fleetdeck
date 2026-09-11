package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/buildinfo"
	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/state"
)

// A panel that cannot take its port says who holds it. Three holders, three
// answers -- and the third, a holder that takes the connection and never
// answers, is why the question has a deadline: without one, a panel meant to
// say "the port is taken" would hang asking who took it.

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func serveOn(t *testing.T, ln net.Listener, h http.Handler) {
	t.Helper()
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestThePortHolderIsNamedWhenItIsAFleetdeckPanel(t *testing.T) {
	ln := listenLoopback(t)
	// The real snapshot type, marshalled the way the panel marshals it, so the
	// field names read here are the ones a panel actually sends.
	body, err := json.Marshal(state.Snapshot{Build: &buildinfo.Fingerprint{
		Web:        "0123abcd",
		Executable: "/Users/op/claude/fleetdeck/bin/fleetdeck.app/Contents/MacOS/fleetdeck",
		Revision:   "97f564b0c1d2e3f4a5b6c7d8e9f00112233445566",
		Modified:   true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	serveOn(t, ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/snapshot" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))

	got := portHolder(context.Background(), ln.Addr().String())
	want := "fleetdeck 97f564b* running from /Users/op/claude/fleetdeck/bin/fleetdeck.app/Contents/MacOS/fleetdeck"
	if got != want {
		t.Fatalf("portHolder = %q, want %q", got, want)
	}
}

func TestThePortHolderIsSaidNotToBeFleetdeckWhenItIsSomethingElse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"a page, not an API", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "<html>not here</html>")
		}, "404 Not Found"},
		{"JSON of some other program", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"ok"}`)
		}, "200 OK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln := listenLoopback(t)
			serveOn(t, ln, tc.handler)
			got := portHolder(context.Background(), ln.Addr().String())
			if !strings.HasPrefix(got, "something that is not a fleetdeck panel") || !strings.Contains(got, tc.want) {
				t.Fatalf("portHolder = %q, want it to say the holder is not fleetdeck, with %q", got, tc.want)
			}
		})
	}
}

func TestAPortHolderThatNeverAnswersCostsNoMoreThanTheDeadline(t *testing.T) {
	ln := listenLoopback(t)
	// Accept every connection and say nothing on it.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()

	start := time.Now()
	got := portHolder(context.Background(), ln.Addr().String())
	took := time.Since(start)

	if !strings.Contains(got, "did not answer within "+holderTimeout.String()) {
		t.Fatalf("portHolder = %q, want it to say the holder did not answer within %s", got, holderTimeout)
	}
	// Both ends: it waited out the deadline -- so the holder really was
	// silent, not refusing -- and not a moment longer.
	if took < holderTimeout*9/10 {
		t.Fatalf("returned after %s, before the %s deadline: the holder was not the silent kind this case is about", took, holderTimeout)
	}
	if took > holderTimeout+500*time.Millisecond {
		t.Fatalf("returned after %s, well past the %s deadline", took, holderTimeout)
	}
}

func TestNobodyHoldingThePortIsSaidSo(t *testing.T) {
	ln := listenLoopback(t)
	addr := ln.Addr().String()
	_ = ln.Close()
	got := portHolder(context.Background(), addr)
	if !strings.HasPrefix(got, "nothing that answers HTTP") {
		t.Fatalf("portHolder = %q, want it to say nothing answers there", got)
	}
}

// The whole panel, not just the helper: run() on a taken port stops with the
// holder named. portHolder being right is no use if run never asks it.
func TestAPanelThatCannotTakeItsPortNamesWhatHoldsIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		serve func(t *testing.T, ln net.Listener)
		want  string
	}{
		{"another fleetdeck panel", func(t *testing.T, ln net.Listener) {
			body, _ := json.Marshal(state.Snapshot{Build: &buildinfo.Fingerprint{Web: "w", Executable: "/app/fleetdeck", Revision: "97f564b0c1d2"}})
			serveOn(t, ln, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
		}, "the port is held by fleetdeck 97f564b running from /app/fleetdeck"},
		// Takes the connection from the backlog and never answers.
		{"something silent", func(*testing.T, net.Listener) {}, "the port is held by something that took the connection and did not answer within " + holderTimeout.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln := listenLoopback(t)
			tc.serve(t, ln)

			cfgPath := filepath.Join(t.TempDir(), "config.yaml")
			cfg := config.Default()
			cfg.ServerPort = ln.Addr().(*net.TCPAddr).Port
			cfg.UsageEnabled = false
			if err := config.Save(cfgPath, cfg); err != nil {
				t.Fatal(err)
			}
			err := run(cfgPath, "", 0)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("run on a taken port: %v; want it to say %q", err, tc.want)
			}
		})
	}
}

func TestTheBindErrorNamesTheHolderAndThisBuild(t *testing.T) {
	msg := bindError("127.0.0.1:7777", fmt.Errorf("address already in use"),
		"fleetdeck 97f564b running from /a/fleetdeck.app/Contents/MacOS/fleetdeck",
		&buildinfo.Fingerprint{Revision: "abc1234def", Executable: "/tree/bin/fleetdeck"}).Error()
	for _, want := range []string{
		"bind 127.0.0.1:7777: address already in use",
		"the port is held by fleetdeck 97f564b running from /a/fleetdeck.app/Contents/MacOS/fleetdeck",
		"this build (fleetdeck abc1234 from /tree/bin/fleetdeck) did not start",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("bind error %q lacks %q", msg, want)
		}
	}
}
