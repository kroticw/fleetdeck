package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
)

// pprofEnv names the address the panel serves Go's own profiles on:
// FLEETDECK_PPROF=127.0.0.1:6060 puts /debug/pprof/ there for as long as the
// panel runs. Unset — which is every ordinary start — nothing is listened on
// and nothing is registered.
//
// It exists because the panel's cost is only visible under a live fleet: the
// collect cycle's price depends on how many sessions are running, how many
// project directories the machine has accumulated and how big the board is,
// none of which a benchmark reproduces. Without this the only way to profile a
// running panel is a build made for the occasion.
const pprofEnv = "FLEETDECK_PPROF"

// startPprof serves the profiles on addr, or does nothing for an empty addr.
// It returns once the listener is open, so a caller that got no error can
// profile immediately rather than racing the first request against the bind.
//
// The address must be a loopback one. The profile handlers answer whoever asks
// with no authentication of any kind, and one request can pin the panel into
// half an hour of CPU profiling, so an address reachable from another machine
// is refused rather than served: a panel that listens on 127.0.0.1 for its own
// interface (spec section 8) must not grow a wider surface through a debugging
// knob.
func startPprof(addr string) error {
	if addr == "" {
		return nil
	}
	if err := checkLoopback(addr); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen for pprof on %s: %w", addr, err)
	}
	log.Printf("fleetdeck: pprof on http://%s/debug/pprof/", ln.Addr())
	go func() {
		if err := http.Serve(ln, mux); err != nil {
			log.Printf("fleetdeck: pprof stopped: %v", err)
		}
	}()
	return nil
}

// checkLoopback reports whether addr names a loopback address. A host that is
// not an IP literal is refused rather than resolved: a name that resolves to
// 127.0.0.1 today can resolve elsewhere tomorrow, and this check has to mean
// the same thing every time the panel starts.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s must be host:port, got %q: %w", pprofEnv, addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%s must name a loopback address, got %q", pprofEnv, host)
	}
	return nil
}

func pprofAddr() string { return os.Getenv(pprofEnv) }
