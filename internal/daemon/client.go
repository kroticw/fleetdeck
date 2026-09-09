// Package daemon implements a client for the fleetdeck daemon's Unix control socket.
//
// The wire protocol (framing, envelope, operations, error codes, the control key's
// rules, and the three observed forms of a session "waiting for a human") is documented
// in full at docs/protocol/daemon-control-socket.md, tracked in this repository. Do not
// look for it under .superpowers/ — that directory is gitignored and carries no
// authoritative information for a public checkout.
package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// maxLineBytes bounds a single line read from the daemon (a response, or the attach
// header). It mirrors the daemon's own request cap of 1MB (see ETOOLARGE) so neither
// side can be made to buffer an unbounded line.
const maxLineBytes = 1024 * 1024

// SocketPath returns the path to the daemon control socket for the current user.
// filepath.Glob returns candidates in lexicographic order, which is not the same as
// "most recently started daemon" — a dead socket left behind by a crashed daemon can
// sort before a live one. Try each candidate and return the first that is both safely
// owned and actually accepts a connection.
func SocketPath() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	pattern := filepath.Join("/tmp", fmt.Sprintf("cc-daemon-%s", currentUser.Uid), "*", "control.sock")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}

	return resolveSocketCandidate(matches)
}

// resolveSocketCandidate tries each candidate socket path in order and returns the
// first that is both safely owned and actually accepts a connection. It is split out
// from SocketPath so the refusal-collection behaviour below can be tested directly,
// without needing to plant sockets under the real, uid-specific /tmp path SocketPath
// globs.
//
// When one or more candidates exist but every one of them is refused on ownership
// grounds, that refusal is returned instead of being silently discarded in favour of
// ErrDaemonUnavailable. Ownership refusal is the one signal that exists for a foreign
// socket planted in the world-writable /tmp ahead of the real daemon — reporting it as
// plain unavailability, indistinguishable from "the daemon just isn't running", would
// throw away the only evidence of a possible attack.
func resolveSocketCandidate(matches []string) (string, error) {
	if len(matches) == 0 {
		return "", ErrDaemonUnavailable
	}

	var refusals []error
	for _, candidate := range matches {
		// /tmp is world-writable: skip any candidate that is not safely owned
		// before even probing it, so a planted socket is never dialed just to
		// check liveness.
		if err := checkSocketOwnership(candidate); err != nil {
			refusals = append(refusals, err)
			continue
		}
		conn, err := net.DialTimeout("unix", candidate, 500*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return candidate, nil
	}

	if len(refusals) > 0 {
		return "", errors.Join(append([]error{ErrDaemonUnavailable}, refusals...)...)
	}
	return "", ErrDaemonUnavailable
}

// checkSocketOwnership verifies that the socket file, and every enclosing directory up
// to (but not including) the first one owned by root, is owned by the current user and
// is not writable by group or other.
//
// /tmp is world-writable, so without this check a local attacker could create the
// daemon's socket directory ahead of the real daemon and have a client hand it the
// control key. The daemon defends its side with a peer-uid check on accept; this is
// the client's symmetric check before it ever connects. A directory owned by root
// (e.g. /tmp itself) is the natural boundary of the walk, but only when it is either
// not writable by group/other, or carries the sticky bit if it is — see
// stickyBitSatisfiesRootBoundary. Without that, "root-owned" alone would not actually
// guarantee the directory is outside a local, non-root attacker's control: /tmp's usual
// mode is 1777 (world-writable), and it is specifically the sticky bit that stops
// another user from deleting or renaming this directory's real owner's entries and
// replacing them with their own — which would otherwise let an attacker recreate the
// daemon's socket directory under their own ownership and still pass every check below
// this boundary.
func checkSocketOwnership(socketPath string) error {
	uid := os.Getuid()
	path := socketPath

	for i := 0; i < 64; i++ {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("checking socket path %s: %w", path, err)
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("cannot determine the owner of %s", path)
		}

		if i > 0 && int(stat.Uid) == 0 {
			if !stickyBitSatisfiesRootBoundary(info.Mode()) {
				return fmt.Errorf("refusing socket %s: %s is root-owned, writable by group or other, and missing the sticky bit", socketPath, path)
			}
			// A root-owned enclosing directory (e.g. /tmp itself) that is either not
			// writable by group/other, or is and carries the sticky bit, is outside a
			// local attacker's control. Nothing further up needs checking.
			return nil
		}

		if int(stat.Uid) != uid {
			return fmt.Errorf("refusing socket %s: %s is owned by a different user", socketPath, path)
		}
		if info.Mode()&0o022 != 0 {
			return fmt.Errorf("refusing socket %s: %s is writable by group or other", socketPath, path)
		}

		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}

	return fmt.Errorf("refusing socket %s: too many parent directories to check", socketPath)
}

// stickyBitSatisfiesRootBoundary reports whether a root-owned enclosing directory is
// safe to treat as the boundary of checkSocketOwnership's walk: either it is not
// writable by group or other at all, or it is and carries the sticky bit, which is what
// stops another local user from deleting or renaming another user's entries inside it
// (the exact defence /tmp's usual 1777 mode relies on).
func stickyBitSatisfiesRootBoundary(mode os.FileMode) bool {
	if mode&0o022 == 0 {
		return true
	}
	return mode&os.ModeSticky != 0
}

// setDeadline sets a deadline on the connection from context, or a fallback based on the client.
func (c *Client) setDeadline(ctx context.Context, conn net.Conn) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(c.defaultDeadline))
	}
}

// ControlKey reads the control key from ~/.claude/daemon/control.key.
//
// Per docs/protocol/daemon-control-socket.md section 6, the key file is expected to be
// mode 0600, owned by the user, inside a 0700 directory. checkKeyFileSecurity is the
// symmetric client-side check for that expectation, parallel to checkSocketOwnership's
// check on the socket path. Any failure here — missing, empty, unreadable, wrong owner,
// or readable/writable by group or other — collapses to the same generic
// ErrNoControlKey: per the same section, the user-facing error for a missing or
// unreadable key names neither the file's path nor its contents, and that same rule
// applies to a key file that fails this security check.
func ControlKey() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", wrapNoControlKey(err)
	}

	keyPath := filepath.Join(homeDir, ".claude", "daemon", "control.key")

	if err := checkKeyFileSecurity(keyPath, os.Getuid()); err != nil {
		return "", wrapNoControlKey(err)
	}

	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", wrapNoControlKey(err)
	}

	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", wrapNoControlKey(errors.New("control key file is empty"))
	}

	return key, nil
}

// errNoControlKeyWithCause wraps ErrNoControlKey with an internal diagnostic cause
// while keeping its Error() text identical to plain ErrNoControlKey. Per
// docs/protocol/daemon-control-socket.md section 6, the user-facing message for a
// missing or unreadable key is fixed and generic and names neither the key file's
// path nor its contents — that requirement is about what the message says, not about
// destroying the cause internally. errors.Is(err, ErrNoControlKey) still succeeds (via
// Is below) and errors.As/errors.Unwrap still reach the wrapped cause, so the five
// distinct causes ControlKey can hit (no $HOME, wrong owner, wrong mode, a read error,
// an empty file) remain distinguishable for diagnosis without ever surfacing in the
// text a user sees. The cause itself must never carry the key's value — none of the
// causes constructed by this package do.
type errNoControlKeyWithCause struct {
	cause error
}

func (e *errNoControlKeyWithCause) Error() string {
	return ErrNoControlKey.Error()
}

func (e *errNoControlKeyWithCause) Is(target error) bool {
	return target == ErrNoControlKey
}

func (e *errNoControlKeyWithCause) Unwrap() error {
	return e.cause
}

// wrapNoControlKey attaches an internal cause to ErrNoControlKey. A nil cause yields
// plain ErrNoControlKey rather than a pointless wrapper around nothing.
func wrapNoControlKey(cause error) error {
	if cause == nil {
		return ErrNoControlKey
	}
	return &errNoControlKeyWithCause{cause: cause}
}

// checkKeyFileSecurity refuses a control key file that is not owned by wantUID, or is
// readable or writable by group or other. wantUID is a parameter, rather than
// checkKeyFileSecurity reading os.Getuid() itself, purely so a test can exercise the
// ownership-mismatch branch without needing a second real user account.
func checkKeyFileSecurity(path string, wantUID int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		// Lstat reports the symlink's own mode, not the target's, and a symlink's mode
		// bits are conventionally 0777 on every platform regardless of what it points
		// to. Without this check, keyFileModeIsSecure below would refuse every symlink
		// with "readable or writable by group or other" — a technically true but
		// misleading cause, since it has nothing to do with the symlink's actual
		// permissions. Refusing a symlink outright is still the right call (it lets the
		// key file's real location, and its own security properties, be something
		// other than what this check just verified), but the internal cause should say
		// so plainly rather than blaming a permission bit that isn't the real reason.
		return errors.New("control key file is a symlink, refusing to use it")
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine the owner of the control key file")
	}

	if int(stat.Uid) != wantUID {
		return errors.New("control key file is owned by a different user")
	}
	if !keyFileModeIsSecure(info.Mode()) {
		return errors.New("control key file is readable or writable by group or other")
	}
	return nil
}

// keyFileModeIsSecure reports whether mode denies every group and other permission
// bit, matching the 0600 docs/protocol/daemon-control-socket.md section 6 documents.
func keyFileModeIsSecure(mode os.FileMode) bool {
	return mode&0o077 == 0
}

// stubKeyFunc is substituted for a nil key function passed to New, so every call site
// that needs a key gets a clean ErrNoControlKey instead of a nil-pointer panic. Reading
// (ListSessions, Ping, ReadScreen) never needs a key at all, so it is unaffected.
func stubKeyFunc() (string, error) {
	return "", ErrNoControlKey
}

// Client represents a connection to the daemon control socket.
type Client struct {
	pathMu          sync.Mutex // guards socketPath; a discoverable client can re-resolve it under concurrent use
	socketPath      string
	discoverable    bool                   // true for a client created with Discover; re-resolves its socket on a dial failure
	resolve         func() (string, error) // re-resolves the socket path; set to SocketPath by Discover, injectable in tests
	keyFunc         func() (string, error)
	protoMu         sync.Mutex    // guards proto; the client is shared between a poller and request handlers
	proto           int           // cached protocol number from ping
	readIdleTimeout time.Duration // idle timeout for ReadScreen (default 300ms)
	defaultDeadline time.Duration // default deadline when context carries none (default 30s)

	// screenDeadline is the ceiling ReadScreen derives its own context deadline from,
	// when the caller's context carries none. It is deliberately its own field, not
	// defaultDeadline: defaultDeadline is sized for request/response operations
	// (ping, list, reply) where 30s is a sensible worst case, but ReadScreen's stream
	// never ends on its own for a working session (a redrawing spinner keeps it from
	// ever going idle), so running it to a 30s ceiling means every poll of a busy
	// session blocks for 30 seconds against a poll_interval whose own default is 2s. A
	// field, rather than a constant, lets tests shorten it without touching
	// defaultDeadline and thereby changing what request/response calls are measuring.
	screenDeadline time.Duration // default 2s
}

// New creates a daemon client bound to an explicit socket path for its whole lifetime.
// If key is nil, a stub is substituted: reading is unaffected, and writing operations
// (SendText, and the auth on attach) degrade to ErrNoControlKey instead of panicking.
//
// A client created this way never re-resolves its socket path. If the daemon restarts
// under a new socket directory, calls against this client keep failing with
// ErrDaemonUnavailable until a new Client is created with the new path. Use Discover
// for a client that should recover from that automatically.
func New(socketPath string, key func() (string, error)) *Client {
	if key == nil {
		key = stubKeyFunc
	}
	return &Client{
		socketPath:      socketPath,
		keyFunc:         key,
		readIdleTimeout: 300 * time.Millisecond,
		defaultDeadline: 30 * time.Second,
		screenDeadline:  2 * time.Second,
	}
}

// Discover creates a client that resolves its socket path via SocketPath and
// re-resolves it exactly once whenever a dial fails, so a daemon restart under a new
// socket directory — its directory name is not stable across restarts — is recovered
// from automatically instead of leaving the client stuck with ErrDaemonUnavailable
// until the process holding it is itself restarted.
func Discover(key func() (string, error)) (*Client, error) {
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	c := New(path, key)
	c.discoverable = true
	c.resolve = SocketPath
	return c, nil
}

// dial connects to the client's current socket path, checking beforehand that it (and
// its enclosing directories) are safely owned. For a discoverable client, a dial
// failure triggers exactly one re-resolution of the socket path (via c.resolve, which
// Discover wires to SocketPath) and one retry — covering a daemon that has restarted
// under a new directory — never a loop. A client created with an explicit path (New,
// not discoverable) never re-resolves.
func (c *Client) dial() (net.Conn, error) {
	path := c.currentSocketPath()

	conn, err := dialChecked(path)
	if err == nil {
		return conn, nil
	}
	if !c.discoverable || c.resolve == nil {
		return nil, err
	}

	newPath, resolveErr := c.resolve()
	if resolveErr != nil {
		return nil, err
	}

	conn, err = dialChecked(newPath)
	if err != nil {
		return nil, err
	}
	c.setSocketPath(newPath)
	return conn, nil
}

// dialChecked applies the ownership check and dials, wrapping a dial failure in
// ErrDaemonUnavailable with its cause. A socket that simply does not exist yet is
// "unavailable", the same as one that refuses the connection; only a socket that
// exists but fails the ownership check is treated as a distinct security refusal.
func dialChecked(path string) (net.Conn, error) {
	if err := checkSocketOwnership(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
		}
		return nil, err
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	return conn, nil
}

func (c *Client) currentSocketPath() string {
	c.pathMu.Lock()
	defer c.pathMu.Unlock()
	return c.socketPath
}

func (c *Client) setSocketPath(path string) {
	c.pathMu.Lock()
	c.socketPath = path
	c.pathMu.Unlock()
}

// ensureProto returns the cached protocol number, pinging the daemon first if it has
// not been negotiated yet. Concurrent first calls may each perform a ping — each ping
// is idempotent and safe to race, so this trades a few duplicate round trips for a
// simple, deadlock-free lock around the cached field.
func (c *Client) ensureProto(ctx context.Context) (int, error) {
	c.protoMu.Lock()
	cached := c.proto
	c.protoMu.Unlock()
	if cached != 0 {
		return cached, nil
	}

	info, err := c.Ping(ctx)
	if err != nil {
		return 0, err
	}
	return info.Proto, nil
}

// invalidateProto clears the cached protocol number so the next call re-negotiates it
// with a fresh ping. Used when the daemon answers EPROTO, which means it has been
// upgraded since the cached number was negotiated.
func (c *Client) invalidateProto() {
	c.protoMu.Lock()
	c.proto = 0
	c.protoMu.Unlock()
}

// isProtoErr reports whether err is the daemon's EPROTO response.
func isProtoErr(err error) bool {
	var protoErr *ErrProto
	return errors.As(err, &protoErr)
}

// writeRequest marshals a request object and appends a newline.
func (c *Client) writeRequest(conn net.Conn, req map[string]interface{}) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	// Append newline
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return err
	}

	return nil
}

// readBoundedLine reads a single '\n'-terminated line, refusing to buffer more than
// maxLineBytes before one is found — mirroring the daemon's own 1MB request cap so a
// malformed or hostile peer cannot make the client hold an unbounded line in memory.
func readBoundedLine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, 256)
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		line = append(line, b)
		if b == '\n' {
			return line, nil
		}
		if len(line) > maxLineBytes {
			return nil, fmt.Errorf("line exceeds %d bytes", maxLineBytes)
		}
	}
}

// readResponse reads and unmarshals a JSON response from the daemon.
func readResponse(conn net.Conn) (map[string]interface{}, error) {
	line, err := readBoundedLine(bufio.NewReader(conn))
	if err != nil {
		return nil, err
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// readAttachHeader reads the JSON header line that opens an attach response and
// returns an error when the daemon refused the attach (e.g. EAUTH, ENOJOB).
func readAttachHeader(reader *bufio.Reader) error {
	line, err := readBoundedLine(reader)
	if err != nil {
		return err
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(line, &resp); err != nil {
		return err
	}

	ok, _ := resp["ok"].(bool)
	if !ok {
		return daemonError(resp)
	}
	return nil
}

// ekickedPrefix is the plain-text marker the daemon writes into an attach stream, in
// place of PTY bytes, when it evicts this connection — another attacher took over, or
// the daemon otherwise dropped it. See docs/protocol/daemon-control-socket.md section 8.
const ekickedPrefix = "EKICKED:"

// maxAttachBytes bounds how much of an attach stream either path (screen-reading or
// key-sending) accumulates before trimming from the front. It is shared so both build
// on the same ceiling instead of drifting (see Finding 4 in the branch review this
// addressed).
const maxAttachBytes = 1024 * 1024

// findKickOpener returns the offset of the kick marker that qualifies as a real kick,
// or -1 if there is none. The daemon writes the marker "in place of" PTY bytes
// immediately before closing the connection (see docs/protocol/daemon-control-socket.md
// section 8), so the marker and its reason are always the *last* bytes of the stream —
// never anchored to offset 0 or to the start of a line. A terminal screen almost never
// ends with a trailing newline: the last bytes in flight are wherever the cursor sits,
// an escape sequence, a prompt, the tail of a line. Requiring the marker to open a line
// missed exactly that, the common case, because the marker lands flush against
// whatever screen bytes were already in the pipe.
//
// Using the *last* occurrence, rather than the first, matters because the same literal
// text can appear earlier in the buffer as ordinary screen content — this very
// document, docs/protocol/daemon-control-socket.md, contains the string "EKICKED:"
// several times, always mid-sentence, and fleetdeck reads the screens of Claude Code
// sessions, one of which may well be editing that document. Taking the first match
// found could pick up such coincidental text and report a garbled or wrong reason (or,
// worse, treat a truncation boundary that happens to leave that text at offset 0 as the
// marker) instead of the real, trailing one. This function only locates a candidate
// position; detectKick still requires the connection to have actually closed right
// after before treating it as a kick, so ordinary screen content containing this text
// on a connection that stays open — the common, non-kicked case — is never mistaken for
// one.
func findKickOpener(data []byte) int {
	return bytes.LastIndex(data, []byte(ekickedPrefix))
}

// parseKickedMarker reports whether data contains the daemon's kick marker at a
// qualifying position (see findKickOpener) and, if so, returns the reason text that
// follows it, trimmed of surrounding whitespace.
func parseKickedMarker(data []byte) (detail string, kicked bool) {
	pos := findKickOpener(data)
	if pos < 0 {
		return "", false
	}
	rest := data[pos+len(ekickedPrefix):]
	return strings.TrimSpace(string(rest)), true
}

// detectKick decides whether an attach stream was actually kicked, given the bytes
// accumulated and whether the connection was observed to close (as opposed to going
// idle or hitting a deadline). Per docs/protocol/daemon-control-socket.md section 8, the
// marker is written "in place of" PTY bytes and is always followed by the connection
// closing — a marker-shaped position with no observed close is never enough on its own,
// since an ordinary, live, polled session's attach connection stays open indefinitely
// (it only closes on an actual kick or the session exiting) and may happen to display
// the literal marker text as part of its own screen content.
func detectKick(data []byte, closed bool) (detail string, kicked bool) {
	if !closed {
		return "", false
	}
	return parseKickedMarker(data)
}

// collectUntilIdleOrClosed reads from reader into an accumulating buffer until: the
// connection is observed to close (a non-timeout read error), the stream has gone idle
// for idleTimeout with no new bytes, or ctx's deadline is reached. The buffer is capped
// at maxBytes, trimming from the front (never splitting a UTF-8 rune) on overflow.
//
// When idleFromStart is false, the idle timer starts only once the first byte has been
// read, letting ctx's deadline alone bound the wait for that first byte — a slow first
// paint (a loaded machine, a large screen buffer) is normal, not idle. ReadScreen wants
// this. When idleFromStart is true, the idle window is in effect from the very first
// call, exactly like an ordinary idle detection with no special-cased "first byte" grace
// period; SendKeys wants this, since silence for the whole window is itself the expected,
// successful outcome for most key deliveries (see docs/protocol/daemon-control-socket.md
// section 3, item 8: there is no per-delivery acknowledgement).
//
// Both ReadScreen and SendKeys build their kick detection on this one routine (paired
// with detectKick) so they cannot drift apart on what counts as "the connection closing"
// or "the marker arrived" — a marker or a close split across two reads is caught either
// way, since data accumulates across calls to this function.
func collectUntilIdleOrClosed(ctx context.Context, conn net.Conn, reader *bufio.Reader, idleTimeout time.Duration, maxBytes int, idleFromStart bool) (data []byte, closed bool) {
	lastReadTime := time.Now()
	gotFirstByte := idleFromStart
	ctxDeadline, hasCtxDeadline := ctx.Deadline()

	// firstByteDeadline bounds the wait for the very first byte when neither a context
	// deadline nor the idle window is yet in effect (only reachable when idleFromStart
	// is false and ctx carries no deadline). It is computed once, here, rather than as
	// time.Now().Add(idleTimeout) inside the loop: recomputing it from "now" on every
	// iteration pushed the deadline forward by another idleTimeout each time a read
	// timed out, so the loop never actually reached it — contradicting the "bounded
	// wait" this is meant to provide and looping forever against a silent connection.
	firstByteDeadline := lastReadTime.Add(idleTimeout)

	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return data, false
		}

		var readDeadline time.Time
		if hasCtxDeadline {
			readDeadline = ctxDeadline
		}
		if gotFirstByte {
			idleDeadline := lastReadTime.Add(idleTimeout)
			if readDeadline.IsZero() || idleDeadline.Before(readDeadline) {
				readDeadline = idleDeadline
			}
		}
		if readDeadline.IsZero() {
			// Neither a context deadline nor an idle window is in effect yet (only
			// reachable when idleFromStart is false and ctx carries no deadline). Use
			// the fixed firstByteDeadline computed once above, rather than a fresh
			// time.Now().Add(idleTimeout) — see its comment for why that recomputation
			// never actually bounded anything.
			readDeadline = firstByteDeadline
		}
		if err := conn.SetReadDeadline(readDeadline); err != nil {
			return data, false
		}

		n, err := reader.Read(buf)
		if n > 0 {
			gotFirstByte = true
			lastReadTime = time.Now()
			data = append(data, buf[:n]...)
			if maxBytes > 0 && len(data) > maxBytes {
				data = trimToRuneBoundary(data[len(data)-maxBytes:])
			}
		}

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if gotFirstByte {
					if time.Since(lastReadTime) >= idleTimeout {
						return data, false
					}
					continue
				}
				// Still waiting for the first byte. hasCtxDeadline == true would have
				// made readDeadline the context's own deadline above, and ctx.Err()
				// catches that at the top of the next iteration; here, with no context
				// deadline, firstByteDeadline is the only thing bounding this wait, so
				// it must be checked explicitly.
				if !hasCtxDeadline && !time.Now().Before(firstByteDeadline) {
					return data, false
				}
				continue
			}
			// A real error (EOF, connection reset, etc.) means the daemon closed the
			// connection.
			return data, true
		}
	}
}

// daemonError converts a daemon error response into a typed error.
func daemonError(resp map[string]interface{}) error {
	code, ok := resp["code"].(string)
	if !ok {
		return errors.New("unknown error")
	}

	switch code {
	case "EPROTO":
		return &ErrProto{}
	case "EAUTH":
		return &ErrAuth{}
	case "EPEERUID":
		return &ErrPeeruid{}
	case "ETOOLARGE":
		return &ErrToolarge{}
	case "ENOJOB":
		return &ErrNojob{}
	case "ENOREPLY":
		return &ErrNoreply{}
	case "ERESPAWNING":
		return &ErrRespawning{}
	case "ESTARTING":
		return &ErrStarting{}
	default:
		return &ErrUnknown{Code: code}
	}
}

// Ping sends a ping request and caches the daemon's protocol version.
func (c *Client) Ping(ctx context.Context) (Info, error) {
	conn, err := c.dial()
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = conn.Close() }()

	c.setDeadline(ctx, conn)

	// Send ping request with no proto field
	req := map[string]interface{}{
		"op": "ping",
	}

	if err := c.writeRequest(conn, req); err != nil {
		return Info{}, err
	}

	resp, err := readResponse(conn)
	if err != nil {
		return Info{}, err
	}

	ok, _ := resp["ok"].(bool)
	if !ok {
		return Info{}, daemonError(resp)
	}

	info := Info{}
	if version, ok := resp["version"].(string); ok {
		info.Version = version
	}

	// A ping reply without a usable proto is an error about the reply itself, not a
	// silent Info{Proto: 0}. Proto 0 is indistinguishable from "not yet negotiated" in
	// the cache (see ensureProto), so accepting it here would make every subsequent
	// call re-ping, roughly doubling request volume against a poller — and it would
	// surface downstream as a spurious "proto mismatch" even though the daemon never
	// actually disagreed about a version.
	protoValue, ok := resp["proto"]
	if !ok {
		return Info{}, errors.New("ping reply missing proto field")
	}
	protoNum, ok := protoValue.(float64)
	if !ok {
		return Info{}, fmt.Errorf("ping reply proto field is not a number: %v", protoValue)
	}
	// A proto of 0 (or negative) is just as unusable as a missing field for the same
	// reason described above: it is indistinguishable from "not yet negotiated" in the
	// cache, so ensureProto would re-ping before every call, and the client would then
	// send "proto": 0 on every subsequent request and earn an EPROTO from the daemon.
	if protoNum < 1 {
		return Info{}, fmt.Errorf("ping reply proto field must be a positive integer, got %v", protoValue)
	}

	info.Proto = int(protoNum)
	c.protoMu.Lock()
	c.proto = info.Proto
	c.protoMu.Unlock()

	return info, nil
}

// ListSessions retrieves the list of active sessions.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	sessions, err := c.listSessionsOnce(ctx)
	if isProtoErr(err) {
		// The daemon was upgraded since we cached its proto; renegotiate and retry
		// exactly once.
		c.invalidateProto()
		sessions, err = c.listSessionsOnce(ctx)
	}
	return sessions, err
}

func (c *Client) listSessionsOnce(ctx context.Context) ([]Session, error) {
	proto, err := c.ensureProto(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	c.setDeadline(ctx, conn)

	// Send list request with proto
	req := map[string]interface{}{
		"proto": proto,
		"op":    "list",
	}

	if err := c.writeRequest(conn, req); err != nil {
		return nil, err
	}

	// Read and unmarshal directly to struct with jobs array
	line, err := readBoundedLine(bufio.NewReader(conn))
	if err != nil {
		return nil, err
	}

	var resp struct {
		Ok   bool       `json:"ok"`
		Jobs *[]Session `json:"jobs"`
		Code string     `json:"code,omitempty"`
	}

	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}

	if !resp.Ok {
		// A reply with no code at all must not become &ErrUnknown{Code: ""}, which
		// daemonError would otherwise happily build — that prints as "unknown error: "
		// with a dangling colon and nothing after it. Every other error path in this
		// function already produces a clean message when there is no code; match that.
		if resp.Code == "" {
			return nil, errors.New("unknown error")
		}
		// Reconstruct error response for daemonError
		errResp := map[string]interface{}{"code": resp.Code}
		return nil, daemonError(errResp)
	}

	// Distinguish between missing jobs key and empty jobs array
	if resp.Jobs == nil {
		return nil, errors.New("daemon reply missing jobs field")
	}

	return *resp.Jobs, nil
}

// SendText sends text into a session via the reply operation.
func (c *Client) SendText(ctx context.Context, session, text string, submit bool) error {
	if !submit {
		return &ErrSubmitNotSupported{}
	}

	err := c.sendTextOnce(ctx, session, text)
	if isProtoErr(err) {
		c.invalidateProto()
		err = c.sendTextOnce(ctx, session, text)
	}
	return err
}

func (c *Client) sendTextOnce(ctx context.Context, session, text string) error {
	// Get the control key first, before making any network calls
	key, err := c.keyFunc()
	if err != nil {
		return wrapNoControlKey(err)
	}

	proto, err := c.ensureProto(ctx)
	if err != nil {
		return err
	}

	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	c.setDeadline(ctx, conn)

	// Send reply request with proto and auth
	req := map[string]interface{}{
		"proto": proto,
		"op":    "reply",
		"short": session,
		"text":  text,
		"auth":  key,
	}

	if err := c.writeRequest(conn, req); err != nil {
		return err
	}

	resp, err := readResponse(conn)
	if err != nil {
		return err
	}

	ok, _ := resp["ok"].(bool)
	if !ok {
		return daemonError(resp)
	}

	return nil
}

// trimToRuneBoundary drops leading bytes that are the tail end of a multi-byte UTF-8
// sequence whose start was cut off, so a byte-oriented truncation never hands the
// caller a slice that begins mid-rune.
//
// It does NOT reconstruct or otherwise protect a truncated ANSI escape sequence. Cutting
// off the front of, say, "\x1b[31m" leaves "[31m" — every byte of which is a perfectly
// valid, ordinary rune, so this function has no reason to touch it — and it will render
// as the literal text "[31m" rather than a colour change. Fixing that would require
// parsing escape sequences, which this function deliberately does not attempt: it only
// protects rune boundaries, nothing more.
func trimToRuneBoundary(b []byte) []byte {
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r != utf8.RuneError || size != 1 {
			break
		}
		b = b[1:]
	}
	return b
}

// ReadScreen sends an attach request and reads the terminal stream.
// It returns the raw bytes that follow the JSON header line.
// tail limits how much of the tail to keep; tail <= 0 means keep everything.
//
// cols/rows are fixed at 80x24 for this attach. This is not an open risk: the daemon's
// own attach handler stores the requested geometry per-attacher
// (attachers.set(id, {cols, rows, ...})) and never calls the session's resize from it.
// Only the separate "resize" operation invokes the session's resize(cols, rows). So
// passing cols/rows on attach has no side effect on anyone's terminal, and a poller
// calling ReadScreen on a cadence cannot reshape a user's session.
func (c *Client) ReadScreen(ctx context.Context, session string, tail int) (string, error) {
	// The read loop below races a single absolute deadline against the idle
	// timeout. When the caller's context carries none (context.Background() is the
	// documented call path for a session poller), derive one here, once, so
	// ctx.Err() is the loop's only exit condition instead of two competing clocks.
	//
	// This uses c.screenDeadline, not c.defaultDeadline: a chatty session (a redrawing
	// spinner, say) never goes idle, so the idle timeout never fires and this derived
	// deadline is what actually ends the read. defaultDeadline's 30s is sized for
	// request/response operations, not for bounding a stream read on every poll tick.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.screenDeadline)
		defer cancel()
	}

	out, err := c.readScreenOnce(ctx, session, tail)
	if isProtoErr(err) {
		c.invalidateProto()
		out, err = c.readScreenOnce(ctx, session, tail)
	}
	return out, err
}

func (c *Client) readScreenOnce(ctx context.Context, session string, tail int) (string, error) {
	proto, err := c.ensureProto(ctx)
	if err != nil {
		return "", err
	}

	conn, err := c.dial()
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	c.setDeadline(ctx, conn)

	// Get control key if available
	key, _ := c.keyFunc()

	// Build attach request. Only include auth if key is available.
	req := map[string]interface{}{
		"proto": proto,
		"op":    "attach",
		"short": session,
		"cols":  80,
		"rows":  24,
	}

	if key != "" {
		req["auth"] = key
	}

	if err := c.writeRequest(conn, req); err != nil {
		return "", err
	}

	// Read the JSON header line; a refused attach (EAUTH, ENOJOB, ...) must surface
	// as a typed error rather than an empty screen.
	reader := bufio.NewReader(conn)
	if err := readAttachHeader(reader); err != nil {
		return "", err
	}

	// Read the streamed bytes with idle detection. The daemon keeps the attach
	// connection open for an ordinary, live session, so this needs to detect when the
	// stream goes idle (no data for c.readIdleTimeout, counted from the first byte
	// received — see collectUntilIdleOrClosed) and return promptly, not wait for the
	// full context deadline; a session that prints continuously (a spinner, say) never
	// goes idle, so the context deadline is what ends the read in that case, and what
	// has been accumulated by then is a real, valid partial screen.
	data, closed := collectUntilIdleOrClosed(ctx, conn, reader, c.readIdleTimeout, maxAttachBytes, false)

	// The kick marker means this attach connection was evicted (see
	// docs/protocol/daemon-control-socket.md section 8). It is only ever a kick when it
	// opens the stream or a line AND the connection actually closed — never merely
	// because the accumulated screen happens to contain that text, which an ordinary,
	// still-open attach reading a live session's screen can do (this very document,
	// displayed by a session working on fleetdeck itself, contains "EKICKED:" four
	// times).
	if detail, kicked := detectKick(data, closed); kicked {
		return "", &ErrKicked{Detail: detail}
	}

	// Apply tail limit if needed
	if tail > 0 && len(data) > tail {
		data = trimToRuneBoundary(data[len(data)-tail:])
	}

	return string(data), nil
}

// SendKeys sends key bytes via an attach connection. This is a write into a live
// session's terminal, exactly like SendText: it requires a control key and returns
// ErrNoControlKey before dialling when none is available, rather than silently
// degrading to some read-only behaviour.
//
// SendKeys must never be retried blindly by a caller. The attach protocol offers no
// per-delivery acknowledgement (see sendKeysOnce), so even the errors it returns do not
// always mean "nothing happened": a write failure ([ErrKeysNotDelivered]) can still have
// delivered a partial prefix of the keys to the daemon before failing. A caller that
// retries on any non-nil error risks typing into the session a second time.
func (c *Client) SendKeys(ctx context.Context, session, keys string) error {
	err := c.sendKeysOnce(ctx, session, keys)
	if isProtoErr(err) {
		// EPROTO always surfaces at (or before) the attach header, strictly before
		// any key bytes are written, so retrying here never double-delivers keys.
		c.invalidateProto()
		err = c.sendKeysOnce(ctx, session, keys)
	}
	return err
}

func (c *Client) sendKeysOnce(ctx context.Context, session, keys string) error {
	// Get the control key first, before making any network calls. SendKeys types into
	// a live session's PTY exactly like SendText types into its prompt; both are
	// writes, and both must be refused the same way when no key is available, rather
	// than silently attaching with no auth and relying on the daemon's peer-uid check
	// alone.
	key, err := c.keyFunc()
	if err != nil {
		return wrapNoControlKey(err)
	}

	proto, err := c.ensureProto(ctx)
	if err != nil {
		return err
	}

	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	c.setDeadline(ctx, conn)

	req := map[string]interface{}{
		"proto": proto,
		"op":    "attach",
		"short": session,
		"cols":  80,
		"rows":  24,
		"auth":  key,
	}

	if err := c.writeRequest(conn, req); err != nil {
		return err
	}

	// Read the JSON header line; a refused attach must not look like delivered input.
	reader := bufio.NewReader(conn)
	if err := readAttachHeader(reader); err != nil {
		return err
	}

	// Write the key bytes. A failure here means delivery could not be confirmed at
	// all — not that nothing happened: a stream socket write can fail after writing a
	// partial prefix of its argument, so the daemon may already have received some of
	// the keys. Report it distinctly from a post-write close (below), which is a
	// confirmed delivery.
	if _, err := conn.Write([]byte(keys)); err != nil {
		return &ErrKeysNotDelivered{Err: err}
	}

	// The attach protocol has no per-delivery acknowledgement past the header:
	// verified against the daemon's own attach handler (CLI 2.1.263) — once the
	// header is accepted, the connection's incoming bytes are wired straight into
	// the session's PTY writer with no reply message of any kind. The only signal
	// observable from here is the connection closing, which happens if another
	// attacher kicks this one or the session exits — not a per-key acknowledgement.
	// Give the daemon a brief window to produce that signal, accumulating across
	// reads with the same routine ReadScreen uses (see collectUntilIdleOrClosed) so a
	// kick marker split across two reads, or arriving after a chunk of ordinary PTY
	// bytes, is never missed the way a single fixed-size read would miss it.
	data, closed := collectUntilIdleOrClosed(ctx, conn, reader, c.readIdleTimeout, maxAttachBytes, true)

	if detail, kicked := detectKick(data, closed); kicked {
		// The keys were written to the connection, but a kick observed right after
		// means another attacher may have taken over before (or as) the daemon
		// applied them — unlike a plain close with no marker, this is a signal worth
		// surfacing distinctly rather than folding into "success".
		return &ErrKicked{Detail: detail}
	}

	// Either silence within the window (the best confirmation this protocol offers for
	// an ordinary delivery), or the connection closing with no kick marker present.
	// The write above already succeeded, so the keys are known to have reached the
	// daemon; the connection closing now — because the session finished its turn, or
	// another attacher took over, both of which can happen as a direct consequence of
	// the very keys just delivered — is a normal outcome, not a delivery failure.
	// Reporting it as an error here would invite a retry at a higher layer, and a
	// retry means typing into a live session twice.
	return nil
}
