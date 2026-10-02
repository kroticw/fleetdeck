package main

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// An ordinary start sets nothing, and must listen on nothing: the profiles are
// a debugging surface, and a panel that opened one by default would carry it on
// every operator's machine.
func TestStartPprofDoesNothingWithoutAnAddress(t *testing.T) {
	t.Parallel()
	if err := startPprof(""); err != nil {
		t.Fatalf("an empty address must be a no-op, got %v", err)
	}
}

func TestStartPprofServesProfiles(t *testing.T) {
	t.Parallel()
	addr := freeLoopbackAddr(t)
	if err := startPprof(addr); err != nil {
		t.Fatal(err)
	}

	// startPprof returns with the listener already open, so no retry loop is
	// needed here — and its absence is what would fail if that ever changed.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/debug/pprof/heap?debug=1")
	if err != nil {
		t.Fatalf("get heap profile: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 from the heap profile, got %d", resp.StatusCode)
	}
}

// The handlers authenticate nobody and one request can hold the panel in a
// long CPU profile, so the knob must not be able to open that to the network.
func TestStartPprofRefusesANonLoopbackAddress(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"0.0.0.0:6060", "192.168.1.10:6060", "localhost:6060"} {
		err := startPprof(addr)
		if err == nil {
			t.Fatalf("%s must be refused", addr)
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("the error must say why %s was refused, got %v", addr, err)
		}
	}
}

func TestStartPprofRefusesAnAddressWithoutAPort(t *testing.T) {
	t.Parallel()
	if err := startPprof("127.0.0.1"); err == nil {
		t.Fatal("an address with no port must be refused")
	}
}

// freeLoopbackAddr is a loopback address nothing is listening on: taken by
// binding and released at once, which is as close to "free port" as the
// operating system offers.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
