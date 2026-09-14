package supervisor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// connCounter follows a test server's connections by their state.
type connCounter struct {
	mu   sync.Mutex
	open map[net.Conn]http.ConnState
}

func (c *connCounter) track(conn net.Conn, state http.ConnState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch state {
	case http.StateClosed, http.StateHijacked:
		delete(c.open, conn)
	default:
		c.open[conn] = state
	}
}

func (c *connCounter) counts() (open, quiet int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.open {
		open++
		if s == http.StateNew {
			quiet++
		}
	}
	return open, quiet
}

// A handover stops the installed panel before its own starts, and the
// installed panel is the version being replaced: v0.9.2 waits, when it stops,
// on every connection it has, one that has asked for nothing included, for up
// to five seconds -- inside the old window's 2.436 s for the whole handover
// (T-060). The new window's probes of a panel leave it no connection to wait
// on, even when several of them probe at once, as a takeover and its keeper do.
func TestProbesOfAPanelLeaveNoConnectionOpen(t *testing.T) {
	cc := &connCounter{open: map[net.Conn]http.ConnState{}}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, `{"build":{"web":"stand-in","executable":"/stand-in","owner":0,"revision":"r"}}`)
	}))
	srv.Config.ConnState = cc.track
	srv.Start()
	t.Cleanup(srv.Close)
	url := srv.URL + "/"

	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				answers(ctx, url)
				holderBuild(ctx, url)
				_, _ = panelRevision(ctx, url)
				_ = WaitAnswer(ctx, url)
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for {
		open, quiet := cc.counts()
		if open == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d connections to the panel still open 2 s after the probes, %d of them never asked for anything", open, quiet)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
