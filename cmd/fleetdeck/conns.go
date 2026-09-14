package main

import (
	"net"
	"net/http"
	"sync"
)

// quietConns is the panel's connections that have not asked for anything yet.
//
// A server shutting down waits for the connections with work in them, and it
// counts a connection that has sent no request yet among them for up to five
// seconds, since it cannot tell one never going to ask from one about to. On
// an update stand of 2026-09-14 (T-060) such a connection -- opened by a probe
// of the new window's and never used -- held the panel being replaced for
// 2113 ms, past the old window's deadline for the whole handover. A panel told
// to stop closes those at once, and any accepted after; a connection with a
// request in work is still waited on, for shutdownTimeout.
type quietConns struct {
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	stopping bool
}

// track is the server's ConnState hook.
func (q *quietConns) track(c net.Conn, state http.ConnState) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if state != http.StateNew {
		// It has asked for something, or it is gone: the server's to wait for
		// or to forget.
		delete(q.conns, c)
		return
	}
	if q.stopping {
		_ = c.Close()
		return
	}
	if q.conns == nil {
		q.conns = map[net.Conn]struct{}{}
	}
	q.conns[c] = struct{}{}
}

// closeAll closes every connection that has asked for nothing, and every one
// accepted from now on.
func (q *quietConns) closeAll() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopping = true
	for c := range q.conns {
		_ = c.Close()
	}
	q.conns = nil
}
