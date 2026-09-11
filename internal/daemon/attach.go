package daemon

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ErrAttachmentClosed is what Resize and Write report once this attacher no longer
// exists on the daemon — closed by its owner, kicked, or ended by the session
// exiting. It is the client's to say, because the daemon will not: a resize
// addressed to an attachId it does not know is answered {"ok":true} and changes
// nothing (measured against CLI 2.1.263, control case included).
var ErrAttachmentClosed = errors.New("attach connection is closed")

// attachReadChunk is how much one Read pulls from the connection at a time.
const attachReadChunk = 32 * 1024

// attachWriteTimeout bounds a single write into the session. A daemon that has
// stopped reading must not leave the writer blocked forever.
const attachWriteTimeout = 5 * time.Second

// Attachment is a held attach connection: a live, two-way view of one session's
// terminal. Unlike ReadScreen, which opens an attach, waits for the stream to go
// quiet and closes it again, an Attachment stays open and hands bytes to its reader
// as they arrive. That is the whole difference between a keystroke echoing in about
// ten milliseconds and a snapshot arriving 300ms to 2s later.
//
// Read is meant for one goroutine at a time; Write, Resize and Close may be called
// from others while it runs.
//
// Its geometry is set once, when it is opened, and after that only by Resize. Both
// travel into the session's real PTY and every other viewer of the session sees them
// — see attachResizesSessionToCols for what the daemon does with a size. Resize
// reaches the daemon as a separate request addressed to this attacher by its own id,
// so it never reconnects, and never costs a second attach that would itself resize
// the session.
type Attachment struct {
	client  *Client
	session string
	id      string
	conn    net.Conn
	reader  *bufio.Reader

	// key is empty when no control key was available at Attach time; keyErr then
	// says why, and every write is refused with it. The connection still reads.
	key    string
	keyErr error

	// Owned by the Read goroutine.
	buf     []byte
	pending []byte // read but not yet released: may be the start of a kick marker
	ready   []byte // released, waiting to be copied out
	end     error  // set once the stream has ended; returned after ready drains

	gone      atomic.Bool // this attacher no longer exists on the daemon
	closeOnce sync.Once
}

// Attach opens a held attach connection to session at the given geometry.
//
// With a control key the connection can type into the session; without one it opens
// anyway, for reading only, and every Write is refused with ErrNoControlKey — never
// silently dropped. A key that is present but wrong is not papered over the same way:
// the daemon refuses such an attach outright and that refusal is returned as *ErrAuth.
//
// ctx bounds opening the connection — dial and the header — and nothing after it.
// The stream itself has no deadline; it ends on Close, a kick, or the session exiting.
func (c *Client) Attach(ctx context.Context, session string, cols, rows int) (*Attachment, error) {
	if err := checkGeometry(cols, rows); err != nil {
		return nil, err
	}
	a, err := c.attachOnce(ctx, session, cols, rows)
	if isProtoErr(err) {
		// EPROTO arrives in the header, before this connection has written a single
		// byte into the session, so retrying cannot type anything twice.
		c.invalidateProto()
		a, err = c.attachOnce(ctx, session, cols, rows)
	}
	return a, err
}

func (c *Client) attachOnce(ctx context.Context, session string, cols, rows int) (*Attachment, error) {
	key, keyErr := c.keyFunc()
	if keyErr != nil {
		key = ""
	}
	id, err := newAttachID()
	if err != nil {
		return nil, err
	}

	proto, err := c.ensureProto(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	c.setDeadline(ctx, conn)

	req := map[string]interface{}{
		"proto":    proto,
		"op":       "attach",
		"short":    session,
		"cols":     cols,
		"rows":     rows,
		"attachId": id,
	}
	// The field is omitted rather than sent empty: the daemon accepts an attach with
	// no auth at all but rejects a wrong one (docs/protocol/daemon-control-socket.md
	// section 3).
	if key != "" {
		req["auth"] = key
	}
	if err := c.writeRequest(conn, req); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	if err := readAttachHeader(reader); err != nil {
		_ = conn.Close()
		return nil, err
	}
	// The deadline governed opening only. A held stream on a quiet session goes minutes
	// without a byte and must not be cut for it; the daemon itself kept one open for 80
	// seconds of total silence in measurement.
	_ = conn.SetDeadline(time.Time{})

	return &Attachment{
		client:  c,
		session: session,
		id:      id,
		conn:    conn,
		reader:  reader,
		key:     key,
		keyErr:  keyErr,
		buf:     make([]byte, attachReadChunk),
	}, nil
}

// Read returns the session's terminal bytes as they arrive.
//
// When the stream ends it returns io.EOF (the session exited or the daemon closed the
// connection), a *ErrKicked (another attacher evicted this one — see detectKick for
// the two conditions that make a close a kick), or the read error itself. A kick's
// marker text never reaches the reader; the screen written before it does.
//
// To recognise a kick at all, bytes that might be the marker must be held back until
// either more output shows they were not, or the connection closes. Holding back is
// kept as narrow as it can be, because anything held is something the operator does
// not see yet:
//
//   - a complete marker with a short, newline-free tail after it is held — it is what
//     the daemon's last write before a close looks like;
//   - the first few bytes of a marker at the very end of what was read are held only
//     when more output is already waiting. A one-byte echo of a typed capital E is
//     also "the first byte of the marker", and holding that until the next keystroke
//     would make the operator's own typing lag.
//
// The accepted cost of the second rule: a marker the kernel splits across two reads
// with nothing else waiting is not recognised, and the stream ends as io.EOF with the
// marker shown as screen. The daemon writes the marker in one short write immediately
// before closing, so this needs the socket buffer to split a few dozen bytes.
func (a *Attachment) Read(p []byte) (int, error) {
	for {
		if len(a.ready) > 0 {
			n := copy(p, a.ready)
			a.ready = a.ready[n:]
			return n, nil
		}
		if a.end != nil {
			return 0, a.end
		}

		n, err := a.reader.Read(a.buf)
		a.pending = append(a.pending, a.buf[:n]...)
		if err != nil {
			a.gone.Store(true)
			if prefix, detail, kicked := detectKick(a.pending, true); kicked {
				a.ready, a.end = prefix, &ErrKicked{Detail: detail}
			} else {
				a.ready, a.end = a.pending, err
			}
			a.pending = nil
			continue
		}

		moreWaiting := n == len(a.buf) || a.reader.Buffered() > 0
		cut := len(a.pending) - kickHoldback(a.pending, moreWaiting)
		a.ready = a.pending[:cut]
		a.pending = append([]byte(nil), a.pending[cut:]...)
	}
}

// kickHoldback returns how many bytes at the end of data must not be released yet.
// See Read for the two rules and why the second one depends on moreWaiting.
func kickHoldback(data []byte, moreWaiting bool) int {
	if pos := findKickOpener(data); pos >= 0 {
		rest := data[pos+len(ekickedPrefix):]
		if len(rest) <= maxKickReasonBytes && !bytes.Contains(rest, []byte("\n")) {
			return len(data) - pos
		}
	}
	if !moreWaiting {
		return 0
	}
	for k := len(ekickedPrefix) - 1; k > 0; k-- {
		if len(data) >= k && bytes.HasSuffix(data, []byte(ekickedPrefix[:k])) {
			return k
		}
	}
	return 0
}

// Writable reports whether this attachment was opened with a control key, and so
// whether Write can type into the session at all. A caller that shows the terminal
// to a person uses it to say so before they start typing, not after.
func (a *Attachment) Writable() bool {
	return a.keyErr == nil
}

// Write types p into the session. There is no acknowledgement past the attach header
// and a write is never retried: a partial write to a stream socket is possible, and a
// retry here would type into someone's session twice (protocol doc, section 3).
func (a *Attachment) Write(p []byte) (int, error) {
	if a.keyErr != nil {
		return 0, wrapNoControlKey(a.keyErr)
	}
	if a.gone.Load() {
		return 0, ErrAttachmentClosed
	}
	_ = a.conn.SetWriteDeadline(time.Now().Add(attachWriteTimeout))
	return a.conn.Write(p)
}

// Resize changes this attacher's geometry without reconnecting.
//
// It is a separate request to the daemon's `resize` operation, addressed by this
// attacher's own id: the daemon updates the attacher's size, resizes the session's PTY
// to it, and repaints this connection. Measured against CLI 2.1.263 by reading the
// session's tty: the PTY follows, the held connection receives a fresh frame and stays
// open. The daemon does not ask for the control key here; it is sent when there is one
// all the same, so a daemon that starts asking finds it.
//
// The daemon answers {"ok":true} to a resize for an attachId it does not know, and does
// nothing. That is why the id is generated here and never taken from outside, and why a
// resize after this attacher is gone is refused with ErrAttachmentClosed instead of
// being sent to succeed silently.
func (a *Attachment) Resize(ctx context.Context, cols, rows int) error {
	if err := checkGeometry(cols, rows); err != nil {
		return err
	}
	if a.gone.Load() {
		return ErrAttachmentClosed
	}
	err := a.resizeOnce(ctx, cols, rows)
	if isProtoErr(err) {
		// A resize is idempotent: sending it twice leaves the same size behind.
		a.client.invalidateProto()
		err = a.resizeOnce(ctx, cols, rows)
	}
	return err
}

func (a *Attachment) resizeOnce(ctx context.Context, cols, rows int) error {
	c := a.client
	proto, err := c.ensureProto(ctx)
	if err != nil {
		return err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	c.setDeadline(ctx, conn)

	req := map[string]interface{}{
		"proto":    proto,
		"op":       "resize",
		"short":    a.session,
		"cols":     cols,
		"rows":     rows,
		"attachId": a.id,
	}
	if a.key != "" {
		req["auth"] = a.key
	}
	if err := c.writeRequest(conn, req); err != nil {
		return err
	}
	resp, err := readResponse(conn)
	if err != nil {
		return err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		return daemonError(resp)
	}
	return nil
}

// Close ends the attach. It is safe to call more than once and from any goroutine; a
// Read blocked in the meantime returns an error satisfying errors.Is(err, net.ErrClosed).
func (a *Attachment) Close() error {
	var err error
	a.closeOnce.Do(func() {
		a.gone.Store(true)
		err = a.conn.Close()
	})
	return err
}

// checkGeometry refuses what the daemon would answer "malformed request: Invalid
// input" — a zero or negative size — before anything is sent. No upper bound is
// enforced here: the daemon's own ceiling was not established by measurement.
func checkGeometry(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("terminal geometry %dx%d: columns and rows must both be positive", cols, rows)
	}
	return nil
}

// newAttachID names one attacher for the daemon. The daemon keys attachers by this
// value and addresses resize by it, so it must be unique per connection and must
// never come from outside this package.
func newAttachID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate attach id: %w", err)
	}
	return "fleetdeck-" + hex.EncodeToString(b[:]), nil
}
