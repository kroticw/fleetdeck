// Package daemontest is a stand-in for the Claude Code daemon's control socket
// (docs/protocol/daemon-control-socket.md): one request line in and one reply
// line out, and for attach a header line followed by the screen's bytes on a
// connection held open.
//
// The daemon client's tests, the panel's tests and the window's CI stand
// (scripts/standdaemon) use this one stand-in, so none of them passes against a
// fake the others no longer agree with.
package daemontest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// Handler answers one request other than ping on the connection it came in on.
// r holds whatever the client writes after the request line. The connection is
// closed when Handler returns.
type Handler func(req map[string]any, c net.Conn, r *bufio.Reader)

// PingReply is what a stand-in answers ping with.
const PingReply = `{"ok":true,"op":"ping","version":"test","proto":1}`

// AttachHeader is an attach header with every field the daemon sends (section
// 3, attach), for a stand-in that shows a screen.
const AttachHeader = `{"ok":true,"op":"attach","imarkNonce":"stand","decModes":{},"via":"stand","booting":false,` +
	`"tempo":"idle","state":"idle","cached":false,"stale":false,"workerCliVersion":"stand"}`

// Daemon is a running stand-in.
type Daemon struct {
	// Socket is the path it listens on.
	Socket string

	listener net.Listener
	mu       sync.Mutex
	seen     []map[string]any
}

// Start listens on socket and serves until Close. Ping is answered here; every
// other request is recorded and handed to handle. A nil handle closes the
// connection without a reply, as the daemon does for an op it does not know.
func Start(socket string, handle Handler) (*Daemon, error) {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	d := &Daemon{Socket: listener.Addr().String(), listener: listener}
	go d.serve(handle)
	return d, nil
}

func (d *Daemon) serve(handle Handler) {
	for {
		conn, err := d.listener.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer func() { _ = c.Close() }()
			r := bufio.NewReader(c)
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			var req map[string]any
			if json.Unmarshal([]byte(line), &req) != nil {
				return
			}
			if req["op"] == "ping" {
				_, _ = fmt.Fprint(c, PingReply+"\n")
				return
			}
			d.mu.Lock()
			d.seen = append(d.seen, req)
			d.mu.Unlock()
			if handle != nil {
				handle(req, c, r)
			}
		}(conn)
	}
}

// Close stops listening. Connections already handed to a handler stay with it:
// a held attach ends when its handler returns.
func (d *Daemon) Close() error { return d.listener.Close() }

// Requests is every request with op the stand-in has been sent, in order.
func (d *Daemon) Requests(op string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []map[string]any
	for _, r := range d.seen {
		if r["op"] == op {
			out = append(out, r)
		}
	}
	return out
}

// Ops answers each op with its own handler and closes the connection on any
// other.
func Ops(handlers map[string]Handler) Handler {
	return func(req map[string]any, c net.Conn, r *bufio.Reader) {
		op, _ := req["op"].(string)
		if h := handlers[op]; h != nil {
			h(req, c, r)
		}
	}
}

// List answers list with jobs: the job records (section 4) as they stand
// inside the reply's array, comma-separated.
func List(jobs string) Handler {
	return func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		_, _ = fmt.Fprintf(c, `{"ok":true,"op":"list","jobs":[%s]}`+"\n", jobs)
	}
}

// Screen answers attach with AttachHeader and screen, then holds the
// connection open until hold is closed, as the daemon holds an attach until the
// session exits. What the client types into it is read and dropped.
func Screen(screen []byte, hold <-chan struct{}) Handler {
	return func(_ map[string]any, c net.Conn, r *bufio.Reader) {
		_, _ = fmt.Fprint(c, AttachHeader+"\n")
		_, _ = c.Write(screen)
		go func() { _, _ = io.Copy(io.Discard, r) }()
		<-hold
	}
}

// Resized answers resize with success, whoever it names: the daemon does the
// same for an attacher it does not know (section 3, attach).
func Resized(_ map[string]any, c net.Conn, _ *bufio.Reader) {
	_, _ = fmt.Fprint(c, `{"ok":true,"op":"resize"}`+"\n")
}

// TB is what the test helpers need of a *testing.T. It is an interface so the
// stand's daemon, which is not a test, builds this package without the testing
// package.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// ShortSocket is a path for a unix socket in a fresh, short directory removed
// when the test ends. t.TempDir() embeds the test's name, and a few directories
// down that overflows macOS's ~104 byte sun_path ("bind: invalid argument").
func ShortSocket(t TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fd")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// StartTest starts a stand-in on a ShortSocket and closes it when the test
// ends. It does not wait for handlers: one held open by the test returns only
// once the test's own cleanup has run, which comes after this one.
func StartTest(t TB, handle Handler) *Daemon {
	t.Helper()
	d, err := Start(ShortSocket(t), handle)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}
