package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDaemon answers ping itself and hands every other request to handle, along
// with the connection it arrived on, so a test can script exactly what an attach
// stream or a resize reply looks like. Every non-ping request is also recorded.
type fakeDaemon struct {
	socket string
	mu     sync.Mutex
	seen   []map[string]any
}

func (f *fakeDaemon) requests(op string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, r := range f.seen {
		if r["op"] == op {
			out = append(out, r)
		}
	}
	return out
}

func startFakeDaemon(t *testing.T, handle func(req map[string]any, c net.Conn, r *bufio.Reader)) *fakeDaemon {
	t.Helper()
	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeDaemon{socket: listener.Addr().String()}
	// No wait for the handlers here: t.Cleanup runs last-registered first, and a
	// handler held open by holdOpen only returns once holdOpen's own cleanup has
	// run — which, registered earlier, comes after this one. Waiting would deadlock.
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
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
					fmt.Fprint(c, `{"ok":true,"op":"ping","version":"test","proto":1}`+"\n")
					return
				}
				f.mu.Lock()
				f.seen = append(f.seen, req)
				f.mu.Unlock()
				handle(req, c, r)
			}(conn)
		}
	}()
	return f
}

// holdOpen keeps a scripted attach connection open until the test ends, the way
// the real daemon holds one: an attach stream only closes on a kick or when the
// session exits.
func holdOpen(t *testing.T) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	return done
}

func keyed(key string) func() (string, error) {
	return func() (string, error) { return key, nil }
}

func noKey() (string, error) { return "", errors.New("control.key: no such file") }

// readWithin reads once from a, failing the test if nothing arrives in time.
// The deadline is what makes "streams immediately" a checkable property rather
// than a hope: a reader that waits for idle or close would miss it.
func readWithin(t *testing.T, a *Attachment, d time.Duration) (string, error) {
	t.Helper()
	type result struct {
		s   string
		err error
	}
	out := make(chan result, 1)
	go func() {
		b := make([]byte, 4096)
		n, err := a.Read(b)
		out <- result{string(b[:n]), err}
	}()
	select {
	case r := <-out:
		return r.s, r.err
	case <-time.After(d):
		t.Fatalf("Read returned nothing within %s", d)
		return "", nil
	}
}

// readAll drains a until it returns an error, collecting what came before it.
func readAll(t *testing.T, a *Attachment) (string, error) {
	t.Helper()
	var sb strings.Builder
	b := make([]byte, 4096)
	for {
		n, err := a.Read(b)
		sb.Write(b[:n])
		if err != nil {
			return sb.String(), err
		}
	}
}

func TestAttachSendsGeometryItsOwnIDAndTheKey(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		<-stop
	})
	a, err := New(f.socket, keyed("secret-key")).Attach(context.Background(), "abc", 132, 41)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if !a.Writable() {
		t.Error("Writable() = false for an attachment opened with a key")
	}

	got := f.requests("attach")
	if len(got) != 1 {
		t.Fatalf("expected one attach request, got %d", len(got))
	}
	req := got[0]
	if req["short"] != "abc" || req["cols"] != float64(132) || req["rows"] != float64(41) {
		t.Errorf("attach request = %v, want short abc at 132x41", req)
	}
	if req["auth"] != "secret-key" {
		t.Errorf("attach auth = %v, want the control key — without it the connection cannot type", req["auth"])
	}
	id, _ := req["attachId"].(string)
	if !strings.HasPrefix(id, "fleetdeck-") || len(id) <= len("fleetdeck-") {
		t.Errorf("attachId = %q, want a generated fleetdeck-… id: resize is addressed by it", id)
	}
}

func TestAttachIDsAreDistinct(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		<-stop
	})
	client := New(f.socket, keyed("k"))
	for i := 0; i < 2; i++ {
		a, err := client.Attach(context.Background(), "abc", 80, 24)
		if err != nil {
			t.Fatalf("Attach: %v", err)
		}
		defer a.Close()
	}
	got := f.requests("attach")
	if len(got) != 2 || got[0]["attachId"] == got[1]["attachId"] {
		t.Fatalf("two attaches must carry two different ids, got %v and %v", got[0]["attachId"], got[1]["attachId"])
	}
}

func TestAttachRefusesNonPositiveGeometryWithoutDialing(t *testing.T) {
	f := startFakeDaemon(t, func(map[string]any, net.Conn, *bufio.Reader) {})
	client := New(f.socket, keyed("k"))
	for _, g := range [][2]int{{0, 24}, {80, 0}, {-1, 24}} {
		if _, err := client.Attach(context.Background(), "abc", g[0], g[1]); err == nil {
			t.Errorf("Attach(%dx%d) succeeded; the daemon answers such a request \"malformed request\"", g[0], g[1])
		}
	}
	if n := len(f.requests("attach")); n != 0 {
		t.Errorf("%d attach requests reached the daemon for refused geometry", n)
	}
}

func TestAttachWithoutKeyReadsButRefusesToWrite(t *testing.T) {
	stop := holdOpen(t)
	typed := make(chan string, 1)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, r *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		fmt.Fprint(c, "screen")
		go func() {
			b := make([]byte, 64)
			n, _ := r.Read(b)
			typed <- string(b[:n])
		}()
		<-stop
	})
	a, err := New(f.socket, noKey).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach without a key must still open for reading: %v", err)
	}
	defer a.Close()

	if a.Writable() {
		t.Error("Writable() = true for an attachment opened without a key")
	}
	if _, has := f.requests("attach")[0]["auth"]; has {
		t.Error("an attach with no key sent an auth field; the daemon rejects a wrong key outright")
	}
	if s, err := readWithin(t, a, time.Second); err != nil || s != "screen" {
		t.Errorf("read = %q, %v; want the screen", s, err)
	}
	if _, err := a.Write([]byte("x")); !errors.Is(err, ErrNoControlKey) {
		t.Errorf("Write without a key = %v, want ErrNoControlKey", err)
	}
	select {
	case s := <-typed:
		t.Errorf("%q reached the session through a connection that has no key", s)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAttachRefusalIsTyped(t *testing.T) {
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":false,"error":"job not found","code":"ENOJOB"}`+"\n")
	})
	_, err := New(f.socket, keyed("k")).Attach(context.Background(), "gone", 80, 24)
	var nojob *ErrNojob
	if !errors.As(err, &nojob) {
		t.Fatalf("Attach to a missing session = %v, want *ErrNojob", err)
	}
}

func TestAttachRetriesOnceOnProtoMismatch(t *testing.T) {
	stop := holdOpen(t)
	var mu sync.Mutex
	attaches := 0
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		mu.Lock()
		attaches++
		first := attaches == 1
		mu.Unlock()
		if first {
			fmt.Fprint(c, `{"ok":false,"error":"proto","code":"EPROTO","proto":1}`+"\n")
			return
		}
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		<-stop
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach after one EPROTO = %v, want success on the retry", err)
	}
	defer a.Close()
	if n := len(f.requests("attach")); n != 2 {
		t.Errorf("attach requests = %d, want exactly 2 (one refused, one retried)", n)
	}
}

// The property the whole live terminal rests on: bytes reach the reader as they
// arrive, while the connection stays open. The screen-reading path waits for the
// stream to go quiet, which is the 300ms-to-2s this replaces.
func TestAttachStreamsBytesWithoutWaitingForQuiet(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		fmt.Fprint(c, "first")
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(c, "second")
		<-stop
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if s, err := readWithin(t, a, 200*time.Millisecond); err != nil || s != "first" {
		t.Fatalf("first read = %q, %v", s, err)
	}
	if s, err := readWithin(t, a, 200*time.Millisecond); err != nil || s != "second" {
		t.Fatalf("second read = %q, %v", s, err)
	}
}

// The deadline that bounds opening must not survive into the stream: a quiet
// session goes minutes without a byte, and the terminal must still be there when
// it speaks again. Shrunk to 100ms here so the test does not wait 30s to prove it.
func TestAttachStreamOutlivesTheOpeningDeadline(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		time.Sleep(400 * time.Millisecond)
		fmt.Fprint(c, "later")
		<-stop
	})
	client := New(f.socket, keyed("k"))
	client.defaultDeadline = 100 * time.Millisecond
	a, err := client.Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if s, err := readWithin(t, a, 2*time.Second); err != nil || s != "later" {
		t.Fatalf("read after a silence longer than the opening deadline = %q, %v; want the bytes", s, err)
	}
}

// A one-byte echo is exactly what a keystroke produces, and "E" is also the
// first byte of the kick marker. Withholding it until more bytes arrive would
// make the operator's own capital E invisible until the next keypress.
func TestAttachDoesNotWithholdATypedLetterThatStartsTheMarker(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		fmt.Fprint(c, "E")
		<-stop
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if s, err := readWithin(t, a, 200*time.Millisecond); err != nil || s != "E" {
		t.Fatalf("read = %q, %v; want the E at once", s, err)
	}
}

func TestAttachKickIsTypedAndItsMarkerNeverReachesTheReader(t *testing.T) {
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		fmt.Fprint(c, "screen before")
		time.Sleep(30 * time.Millisecond)
		fmt.Fprint(c, "EKICKED: Session opened in another window")
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	got, err := readAll(t, a)
	var kicked *ErrKicked
	if !errors.As(err, &kicked) {
		t.Fatalf("stream ended with %v, want *ErrKicked", err)
	}
	if kicked.Detail != "Session opened in another window" {
		t.Errorf("kick detail = %q", kicked.Detail)
	}
	if got != "screen before" {
		t.Errorf("reader got %q; the screen before the marker must arrive, the marker must not", got)
	}
}

// The same marker text, shown by a session as ordinary output and followed by
// more screen, then an unrelated close: not a kick (protocol doc section 8).
func TestAttachMarkerShownOnScreenIsNotAKick(t *testing.T) {
	const screen = "$ grep EKICKED: docs\nEKICKED: marker\n$ exit\n"
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		fmt.Fprint(c, screen)
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	got, err := readAll(t, a)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("stream ended with %v, want io.EOF", err)
	}
	if got != screen {
		t.Errorf("reader got %q, want the whole screen %q", got, screen)
	}
}

// The same marker shown as ordinary output on a session that stays open: it must
// reach the reader at once. The final output alone cannot tell this apart from a
// reader that holds such a screen back until the connection closes — only the
// wait can, which on a live session could be forever.
func TestAttachMarkerShownOnScreenIsNotHeldBack(t *testing.T) {
	stop := holdOpen(t)
	const screen = "EKICKED: marker\n$ "
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		fmt.Fprint(c, screen)
		<-stop
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if s, err := readWithin(t, a, 200*time.Millisecond); err != nil || s != screen {
		t.Fatalf("read = %q, %v; want the whole screen at once", s, err)
	}
}

func TestAttachWriteReachesTheSession(t *testing.T) {
	stop := holdOpen(t)
	typed := make(chan string, 1)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, r *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		b := make([]byte, 64)
		n, _ := r.Read(b)
		typed <- string(b[:n])
		<-stop
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if _, err := a.Write([]byte("hello\r")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case s := <-typed:
		if s != "hello\r" {
			t.Errorf("session received %q", s)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing reached the session")
	}
}

func TestResizeIsAddressedToThisAttacher(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(req map[string]any, c net.Conn, _ *bufio.Reader) {
		switch req["op"] {
		case "attach":
			fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
			<-stop
		case "resize":
			fmt.Fprint(c, `{"ok":true,"op":"resize"}`+"\n")
		}
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if err := a.Resize(context.Background(), 130, 35); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	attach := f.requests("attach")[0]
	resize := f.requests("resize")
	if len(resize) != 1 {
		t.Fatalf("resize requests = %d, want 1", len(resize))
	}
	r := resize[0]
	if r["attachId"] != attach["attachId"] {
		t.Errorf("resize attachId = %v, attach carried %v — a resize with any other id is answered ok and does nothing",
			r["attachId"], attach["attachId"])
	}
	if r["short"] != "abc" || r["cols"] != float64(130) || r["rows"] != float64(35) {
		t.Errorf("resize request = %v, want short abc at 130x35", r)
	}
}

// Once this attacher is gone, the daemon would answer a resize for its id with
// ok and change nothing (measured). The client is the only one who can say so.
func TestResizeAfterCloseRefusesInsteadOfSucceedingSilently(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(req map[string]any, c net.Conn, _ *bufio.Reader) {
		switch req["op"] {
		case "attach":
			fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
			<-stop
		case "resize":
			fmt.Fprint(c, `{"ok":true,"op":"resize"}`+"\n")
		}
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_ = a.Close()
	if err := a.Resize(context.Background(), 100, 30); !errors.Is(err, ErrAttachmentClosed) {
		t.Errorf("Resize after Close = %v, want ErrAttachmentClosed", err)
	}
	if n := len(f.requests("resize")); n != 0 {
		t.Errorf("%d resize requests sent for an attacher that no longer exists", n)
	}
}

// A stream that ended on its own — the session exited — leaves no attacher behind
// on the daemon either, so the same silent-success trap applies as after Close.
func TestResizeAfterTheStreamEndedRefuses(t *testing.T) {
	f := startFakeDaemon(t, func(req map[string]any, c net.Conn, _ *bufio.Reader) {
		switch req["op"] {
		case "attach":
			fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
			fmt.Fprint(c, "bye\n")
		case "resize":
			fmt.Fprint(c, `{"ok":true,"op":"resize"}`+"\n")
		}
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if _, err := readAll(t, a); !errors.Is(err, io.EOF) {
		t.Fatalf("stream ended with %v, want io.EOF", err)
	}
	if err := a.Resize(context.Background(), 100, 30); !errors.Is(err, ErrAttachmentClosed) {
		t.Errorf("Resize after the session ended = %v, want ErrAttachmentClosed", err)
	}
	if n := len(f.requests("resize")); n != 0 {
		t.Errorf("%d resize requests sent for an attacher that no longer exists", n)
	}
}

func TestWriteAfterCloseRefuses(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(_ map[string]any, c net.Conn, _ *bufio.Reader) {
		fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
		<-stop
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_ = a.Close()
	if _, err := a.Write([]byte("x")); !errors.Is(err, ErrAttachmentClosed) {
		t.Errorf("Write after Close = %v, want ErrAttachmentClosed", err)
	}
}

func TestResizeRefusesNonPositiveGeometry(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(req map[string]any, c net.Conn, _ *bufio.Reader) {
		if req["op"] == "attach" {
			fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
			<-stop
		}
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	if err := a.Resize(context.Background(), 0, 0); err == nil {
		t.Error("Resize(0x0) succeeded; the daemon answers it \"malformed request\"")
	}
	if n := len(f.requests("resize")); n != 0 {
		t.Errorf("%d resize requests reached the daemon for refused geometry", n)
	}
}

func TestResizeRefusalIsTyped(t *testing.T) {
	stop := holdOpen(t)
	f := startFakeDaemon(t, func(req map[string]any, c net.Conn, _ *bufio.Reader) {
		switch req["op"] {
		case "attach":
			fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
			<-stop
		case "resize":
			fmt.Fprint(c, `{"ok":false,"error":"job not found","code":"ENOJOB"}`+"\n")
		}
	})
	a, err := New(f.socket, keyed("k")).Attach(context.Background(), "abc", 80, 24)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer a.Close()
	var nojob *ErrNojob
	if err := a.Resize(context.Background(), 100, 30); !errors.As(err, &nojob) {
		t.Errorf("Resize for a session that exited = %v, want *ErrNojob", err)
	}
}
