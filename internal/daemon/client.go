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
	"math"
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
// from SocketPath so the error-collection behaviour below can be tested directly,
// without needing to plant sockets under the real, uid-specific /tmp path SocketPath
// globs.
//
// When one or more candidates exist but none of them yields a connection, whatever
// caused each one to fail — an ownership refusal, or a dial error — is returned
// instead of being silently discarded in favour of a bare ErrDaemonUnavailable.
// Ownership refusal is the one signal that exists for a foreign socket planted in the
// world-writable /tmp ahead of the real daemon; a dial error is the only diagnostic
// evidence available when a socket file exists but nothing is listening on it (a
// crashed daemon's stale socket, say). Reporting either as plain unavailability,
// indistinguishable from "the daemon just isn't running", would throw away the only
// evidence available for either case.
func resolveSocketCandidate(matches []string) (string, error) {
	if len(matches) == 0 {
		return "", ErrDaemonUnavailable
	}

	var causes []error
	for _, candidate := range matches {
		// /tmp is world-writable: skip any candidate that is not safely owned
		// before even probing it, so a planted socket is never dialed just to
		// check liveness.
		if err := checkSocketOwnership(candidate); err != nil {
			causes = append(causes, err)
			continue
		}
		conn, err := net.DialTimeout("unix", candidate, 500*time.Millisecond)
		if err != nil {
			causes = append(causes, err)
			continue
		}
		_ = conn.Close()
		return candidate, nil
	}

	if len(causes) > 0 {
		return "", errors.Join(append([]error{ErrDaemonUnavailable}, causes...)...)
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
//
// The socket file itself is checked directly, with a plain Lstat: it must never be a
// symlink, regardless of who owns that symlink, since a socket file is exactly what
// this function exists to authenticate before dialling it. The enclosing directory
// chain is a different matter: on macOS, /tmp — the real daemon's socket directory,
// cc-daemon-<uid>, lives directly under it — is itself a symlink to /private/tmp, owned
// by root. An unprivileged local attacker cannot replace a root-owned symlink any more
// than a root-owned directory, so refusing it protects nothing and would make this
// function refuse the one path shape the daemon actually uses in production. A symlink
// owned by anyone else, at any level of the chain, is refused outright: see
// checkSocketOwnershipWalk.
func checkSocketOwnership(socketPath string) error {
	uid := os.Getuid()

	sockStat, err := realLstat(socketPath)
	if err != nil {
		return fmt.Errorf("checking socket path %s: %w", socketPath, err)
	}
	if sockStat.symlink {
		return fmt.Errorf("refusing socket %s: %s is a symlink, refusing to follow it", socketPath, socketPath)
	}
	if sockStat.uid != uid {
		return fmt.Errorf("refusing socket %s: %s is owned by a different user", socketPath, socketPath)
	}
	if sockStat.mode&0o022 != 0 {
		return fmt.Errorf("refusing socket %s: %s is writable by group or other", socketPath, socketPath)
	}

	return checkSocketOwnershipWalk(socketPath, filepath.Dir(socketPath), uid, realLstat)
}

// dirStat is the minimal ownership and mode information checkSocketOwnershipWalk needs
// about one path component, decoupled from os.FileInfo (whose Sys() returns a
// platform-specific *syscall.Stat_t) so a test can drive an exact, host-independent
// directory shape through the injectable lstatFunc below.
type dirStat struct {
	uid     int
	mode    os.FileMode
	symlink bool
	target  string // set only when symlink is true; the raw (possibly relative) link target
}

// lstatFunc abstracts the lstat-plus-readlink pair checkSocketOwnershipWalk needs at
// each path component. realLstat is the production implementation; tests substitute a
// fake to drive a specific directory shape (e.g. macOS's /tmp -> /private/tmp) without
// depending on the host's actual filesystem.
type lstatFunc func(path string) (dirStat, error)

// realLstat is the production lstatFunc, backed by os.Lstat and os.Readlink.
func realLstat(path string) (dirStat, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return dirStat{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return dirStat{}, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return dirStat{}, fmt.Errorf("cannot determine the owner of %s", path)
		}
		return dirStat{uid: int(stat.Uid), symlink: true, target: target}, nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return dirStat{}, fmt.Errorf("cannot determine the owner of %s", path)
	}
	return dirStat{uid: int(stat.Uid), mode: info.Mode()}, nil
}

// checkSocketOwnershipWalk walks the directory chain starting at start (checkSocketOwnership
// calls it with socketPath's parent), moving upward via filepath.Dir exactly as before,
// except that it now resolves a symlink encountered mid-walk instead of refusing every
// symlink unconditionally:
//
//   - a symlink owned by root is followed. Root cannot be impersonated by an
//     unprivileged local attacker, so a root-owned symlink (macOS's /tmp -> /private/tmp
//     is exactly this) carries no more risk than a root-owned directory, and the walk
//     continues from its target.
//   - a symlink owned by anyone else is refused outright. This walk has no way to tell
//     a legitimate symlink from one planted by a local attacker, and the daemon's own
//     directory chain is never expected to contain one, so refusing is the only safe
//     default.
//   - an ordinary directory is checked exactly as before checkSocketOwnershipWalk
//     existed: root-owned marks the boundary of the walk (subject to
//     stickyBitSatisfiesRootBoundary), anything else must be owned by uid and must not
//     be writable by group or other.
func checkSocketOwnershipWalk(socketPath, start string, uid int, lstat lstatFunc) error {
	path := start
	for i := 0; i < 64; i++ {
		info, err := lstat(path)
		if err != nil {
			return fmt.Errorf("checking socket path %s: %w", socketPath, err)
		}

		if info.symlink {
			if info.uid != 0 {
				return fmt.Errorf("refusing socket %s: %s is a symlink owned by a non-root user, refusing to follow it", socketPath, path)
			}
			target := info.target
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			path = filepath.Clean(target)
			continue
		}

		if info.uid == 0 {
			if !stickyBitSatisfiesRootBoundary(info.mode) {
				return fmt.Errorf("refusing socket %s: %s is root-owned, writable by group or other, and missing the sticky bit", socketPath, path)
			}
			// A root-owned enclosing directory (e.g. /private/tmp, the real target of
			// macOS's /tmp symlink) that is either not writable by group/other, or is
			// and carries the sticky bit, is outside a local attacker's control.
			// Nothing further up needs checking.
			return nil
		}

		if info.uid != uid {
			return fmt.Errorf("refusing socket %s: %s is owned by a different user", socketPath, path)
		}
		if info.mode&0o022 != 0 {
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

// dialTimeout bounds the connect(2) call itself, independently of whatever deadline
// (if any) ctx carries. context.Background() is the documented call path for
// ListSessions, SendText, SendKeys and Ping — only ReadScreen derives a context
// deadline of its own — so a Dialer with no Timeout of its own left the connect phase
// completely unbounded on every one of those paths: a daemon that is alive but not
// accepting connections (a full backlog, a wedged process) can make connect(2) on an
// AF_UNIX socket block indefinitely, and nothing could break a caller's goroutine out
// of that. resolveSocketCandidate already dials its liveness probes with
// net.DialTimeout for exactly this reason; dialTimeout is the same defence carried to
// the main connect path.
const dialTimeout = 5 * time.Second

// dial connects to the client's current socket path, checking beforehand that it (and
// its enclosing directories) are safely owned. For a discoverable client, a dial
// failure triggers exactly one re-resolution of the socket path (via c.resolve, which
// Discover wires to SocketPath) and one retry — covering a daemon that has restarted
// under a new directory — never a loop. A client created with an explicit path (New,
// not discoverable) never re-resolves.
//
// The connect itself is bounded twice over: by dialTimeout, set on the Dialer and thus
// in effect no matter what ctx carries, and by ctx's own deadline when it has one
// (DialContext still honours ctx.Done() independently of Timeout). Neither on its own
// used to be enough — ctx alone because context.Background() is the common case, and
// nothing prior set a Dialer.Timeout at all.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	path := c.currentSocketPath()

	conn, err := dialChecked(ctx, path)
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

	conn, err = dialChecked(ctx, newPath)
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
//
// The underlying cause is wrapped with its own %w (not %v), alongside
// ErrDaemonUnavailable, so a context error (context.Canceled,
// context.DeadlineExceeded) surfacing from DialContext below remains reachable via
// errors.Is/As by a caller, not just by string inspection.
func dialChecked(ctx context.Context, path string) (net.Conn, error) {
	if err := checkSocketOwnership(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %w", ErrDaemonUnavailable, err)
		}
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDaemonUnavailable, err)
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
// on the same ceiling instead of drifting apart.
const maxAttachBytes = 1024 * 1024

// kickSearchWindow bounds how far from the end of the accumulated stream the kick
// marker may be found and still be considered at all. The daemon writes the marker
// immediately before closing the connection, so a genuine kick's marker is always
// within a small distance of the very end of what was received. Searching the whole
// buffer instead would let a marker buried under a large amount of ordinary screen
// content that arrived afterward — the same literal text can legitimately appear
// there — be reconsidered as the kick just because the connection happened to close
// later, for an unrelated reason.
const kickSearchWindow = 512

// maxKickReasonBytes bounds how long the text following the marker may be and still
// pass as the daemon's own short, human-readable reason. See the second and third
// conditions in detectKick's comment.
const maxKickReasonBytes = 256

// findKickOpener returns the offset of the kick marker's last occurrence within
// kickSearchWindow bytes of the end of data, or -1 if there is none there. See
// kickSearchWindow's comment for why the search is bounded at all, and detectKick's
// comment for the full compound rule this is one third of.
func findKickOpener(data []byte) int {
	start := 0
	if len(data) > kickSearchWindow {
		start = len(data) - kickSearchWindow
	}
	rel := bytes.LastIndex(data[start:], []byte(ekickedPrefix))
	if rel < 0 {
		return -1
	}
	return start + rel
}

// parseKickedMarker reports whether data contains the daemon's kick marker at a
// qualifying position (see findKickOpener) followed by text that looks like a real
// reason rather than a screen (see detectKick). On success it returns the prefix —
// everything before the marker, i.e. the screen accumulated up to that point — and the
// reason text, trimmed of surrounding whitespace.
func parseKickedMarker(data []byte) (prefix []byte, detail string, kicked bool) {
	pos := findKickOpener(data)
	if pos < 0 {
		return nil, "", false
	}
	rest := data[pos+len(ekickedPrefix):]
	// A real "EKICKED: <reason>" is the very last thing the daemon sends before
	// closing, so nothing of substance follows the reason itself. A newline in rest,
	// or a rest this long, means what matched is ordinary screen content that happens
	// to contain "EKICKED:" followed by more screen — exactly the false positive this
	// rule exists to rule out.
	if len(rest) > maxKickReasonBytes || bytes.Contains(rest, []byte("\n")) {
		return nil, "", false
	}
	return data[:pos], strings.TrimSpace(string(rest)), true
}

// detectKick decides whether an attach stream was actually kicked, given the bytes
// accumulated and whether the connection was observed to close (as opposed to going
// idle or hitting a deadline). On a real kick it also returns prefix, the screen
// accumulated before the marker, so a caller need not discard it.
//
// Per docs/protocol/daemon-control-socket.md section 8, detection must never fire on the
// marker's mere presence, or even on its *last occurrence* alone — only on it being the
// *last thing sent*, immediately before a close. Each weaker rule fails in a specific,
// observable way: anchoring to offset 0 or to a line boundary misses the marker
// entirely, since the daemon writes it flush against whatever PTY bytes were already in
// flight, never anchored to a line boundary — see condition 2. Accepting the marker's
// last occurrence plus a close, with no check on what follows it, fires on a screen that
// merely *displays* the marker text and then exits normally for an unrelated reason — a
// session grepping this very document for "EKICKED:" is a real example — see condition
// 3. All three conditions below are required together; each is individually
// insufficient:
//
//  1. The connection actually closed. An ordinary, live, polled session's attach
//     connection stays open indefinitely (it only closes on an actual kick or the
//     session exiting), and may happen to display the literal marker text as part of
//     its own screen content — see TestReadScreenMidScreenEkickedTextIsNotAKick. Without
//     this, any screen containing the text at all would be misreported as a kick.
//  2. The marker sits within the last kickSearchWindow bytes of the stream (see
//     findKickOpener). The daemon writes the marker flush against whatever PTY bytes
//     were already in flight, immediately before closing — never anchored to offset 0
//     or a line boundary — so a genuine kick's marker is always near the very end.
//     Without this bound, a marker that appears well before the end, in ordinary
//     screen content, could still be picked up just because the connection later
//     closed for an unrelated reason.
//  3. What follows the marker is short and contains no newline (see parseKickedMarker).
//     The daemon's real reason text is the last thing it sends; a screen that merely
//     *displays* the marker almost always has more rendered content after it,
//     typically containing at least one newline. This is exactly what condition 2
//     alone does not catch when the coincidental marker happens to sit close to the
//     end — see TestReadScreenGrepDisplayingMarkerThenExitIsNotAKick, where the last
//     occurrence of "EKICKED:" is immediately followed by " marker\n$ exit\n".
//
// Condition 1 has a deliberate, accepted residual risk: it requires closed to have been
// observed, not merely a marker sitting at the very end of an idle or deadline-truncated
// buffer. If the daemon writes the marker and closes the connection, but the EOF that
// close produces is not read before ReadScreen's own ceiling (screenDeadline, 2s by
// default) fires, collectUntilIdleOrClosed reports closed == false and this function
// declines to call it a kick — the marker's bytes are then returned as ordinary screen
// content instead of ErrKicked. This is deliberately not fixed by treating an
// end-of-buffer marker as sufficient on its own, because that reintroduces exactly the
// failure mode conditions 2 and 3 exist to rule out (a screen that merely displays the
// marker and then goes idle for an unrelated reason). The risk is accepted because the
// daemon writes the marker immediately before closing — the two arrive in close
// succession on the wire — so a close that is not observed within a multi-second
// ceiling is expected to be rare; see TestReadScreenMarkerAtEndWithoutObservedCloseIsNotAKick
// for the documented, tested behaviour in that case.
func detectKick(data []byte, closed bool) (prefix []byte, detail string, kicked bool) {
	if !closed {
		return nil, "", false
	}
	return parseKickedMarker(data)
}

// collectUntilIdleOrClosed reads from reader into an accumulating buffer until: the
// connection is observed to close (a non-timeout read error), the stream has gone idle
// for idleTimeout with no new bytes, ctx's deadline is reached, or until is reached. The
// buffer is capped at maxBytes, trimming from the front (never splitting a UTF-8 rune)
// on overflow.
//
// until is a hard ceiling on the whole call, independent of ctx and of idleTimeout: it
// is checked directly, not derived from lastReadTime, so it cannot be pushed back by a
// session that keeps printing more often than idleTimeout. Without it, a chatty session
// (one that redraws faster than idleTimeout) resets the sliding idle deadline on every
// byte and, when ctx carries no deadline of its own (context.Background(), SendKeys'
// documented call path), nothing ever ends the loop — the call blocks for as long as the
// session keeps talking, or forever. A zero until means no such ceiling is in effect;
// both of this function's callers always pass a non-zero one today.
//
// When idleFromStart is false, the idle timer starts only once the first byte has been
// read, letting ctx's deadline (or until) alone bound the wait for that first byte — a
// slow first paint (a loaded machine, a large screen buffer) is normal, not idle.
// ReadScreen wants this. When idleFromStart is true, the idle window is in effect from
// the very first call, exactly like an ordinary idle detection with no special-cased
// "first byte" grace period; SendKeys wants this, since silence for the whole window is
// itself the expected, successful outcome for most key deliveries (see
// docs/protocol/daemon-control-socket.md section 3, item 8: there is no per-delivery
// acknowledgement).
//
// Both ReadScreen and SendKeys build their kick detection on this one routine (paired
// with detectKick) so they cannot drift apart on what counts as "the connection closing"
// or "the marker arrived" — a marker or a close split across two reads is caught either
// way, since data accumulates across calls to this function. They share the same
// until-based hard ceiling for the same reason: a bound that lives in only one of two
// otherwise-identical call paths is a bound the other path does not actually have.
func collectUntilIdleOrClosed(ctx context.Context, conn net.Conn, reader *bufio.Reader, idleTimeout time.Duration, maxBytes int, idleFromStart bool, until time.Time) (data []byte, closed bool) {
	lastReadTime := time.Now()
	gotFirstByte := idleFromStart
	ctxDeadline, hasCtxDeadline := ctx.Deadline()
	hasUntil := !until.IsZero()

	// firstByteDeadline bounds the wait for the very first byte when neither a context
	// deadline nor the idle window is yet in effect (only reachable when idleFromStart
	// is false and ctx carries no deadline). It is computed once, here, rather than as
	// time.Now().Add(idleTimeout) inside the loop: recomputing it from "now" on every
	// iteration pushed the deadline forward by another idleTimeout each time a read
	// timed out, so the loop never actually reached it — contradicting the "bounded
	// wait" this is meant to provide and looping forever against a silent connection.
	firstByteDeadline := lastReadTime.Add(idleTimeout)
	if hasUntil && until.Before(firstByteDeadline) {
		firstByteDeadline = until
	}

	buf := make([]byte, 4096)
	for {
		if ctx.Err() != nil {
			return data, false
		}
		// until is checked directly against the clock, not folded into lastReadTime-
		// relative math, precisely so a stream of incoming bytes can never push it back
		// — see this function's own doc comment above.
		if hasUntil && !time.Now().Before(until) {
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
		if hasUntil && until.Before(readDeadline) {
			readDeadline = until
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
			// errors.As, not a plain err.(net.Error) type assertion: the latter only
			// works today because bufio.Reader.Read happens to return the transport
			// error unwrapped. A wrapped timeout error would fall straight into the
			// "connection closed" branch below and, combined with the kick detection
			// this feeds, turn an ordinary timeout into a false kick.
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
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
//
// A "code" that is absent entirely, not a string, or present as an empty string, must
// all collapse to the same clean "unknown error" — never "unknown error: " with a
// dangling colon and nothing after it. The empty-string case is not merely academic:
// listSessionsOnce used to guard against it with its own separate check before calling
// this function, which meant every other caller (Ping, sendTextOnce,
// readAttachHeader) that reaches daemonError with an "ok":false, code-less reply did
// not get the same protection. The guard now lives here, once, so it can't drift out
// of sync with any one call site again.
func daemonError(resp map[string]interface{}) error {
	code, _ := resp["code"].(string)
	if code == "" {
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
	conn, err := c.dial(ctx)
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
	// A fractional value (e.g. 1.9) is rejected the same way, rather than silently
	// truncated by int(protoNum) below: the envelope requires proto to be an integer
	// compared strictly against the daemon's own, so a fractional reply is malformed,
	// not a value this client should quietly round down to something the daemon never
	// actually reported.
	if protoNum < 1 || protoNum != math.Trunc(protoNum) {
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

	conn, err := c.dial(ctx)
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
		// daemonError itself now cleanly handles an empty/absent code (see its comment),
		// so there is no need for a separate guard here that could drift out of sync
		// with it again.
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

	conn, err := c.dial(ctx)
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
// tail limits how much of the tail to keep; tail <= 0 keeps everything read, which is
// itself never more than maxAttachBytes (1 MB) — collectUntilIdleOrClosed enforces that
// cap regardless of tail. This limit applies identically whether or not the stream ends
// in a kick (see ErrKicked): a caller that asked for the last few bytes gets the last
// few bytes of the accumulated prefix either way, not the whole thing.
//
// cols/rows are fixed at 80x24 for this attach. This is not an open risk: the daemon's
// own attach handler stores the requested geometry per-attacher
// (attachers.set(id, {cols, rows, ...})) and never calls the session's resize from it.
// Only the separate "resize" operation invokes the session's resize(cols, rows). So
// passing cols/rows on attach has no side effect on anyone's terminal, and a poller
// calling ReadScreen on a cadence cannot reshape a user's session.
func (c *Client) ReadScreen(ctx context.Context, session string, tail int) (string, error) {
	out, err := c.readScreenWithDeadline(ctx, session, tail)
	if isProtoErr(err) {
		c.invalidateProto()
		out, err = c.readScreenWithDeadline(ctx, session, tail)
	}
	return out, err
}

// readScreenWithDeadline derives a fresh, full c.screenDeadline-based context for
// exactly one call to readScreenOnce, when ctx (the caller's own, as passed to
// ReadScreen, never mutated in place) carries no deadline of its own. It is factored
// out of ReadScreen precisely so an EPROTO retry gets a full budget of its own: the
// previous shape derived the deadline once, before the first attempt, and reused that
// same context for the retry — leaving it with whatever time happened to remain after
// the first attempt's own dial-and-response, sometimes almost none.
//
// The read loop below (inside readScreenOnce, via collectUntilIdleOrClosed) races this
// absolute deadline against the idle timeout. context.Background() is the documented
// call path for a session poller, so ctx.Deadline() is typically absent and this
// derivation runs on every attempt. This uses c.screenDeadline, not c.defaultDeadline:
// a chatty session (a redrawing spinner, say) never goes idle, so the idle timeout
// never fires and this derived deadline is what actually ends the read.
// defaultDeadline's 30s is sized for request/response operations, not for bounding a
// stream read on every poll tick.
func (c *Client) readScreenWithDeadline(ctx context.Context, session string, tail int) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.screenDeadline)
		defer cancel()
	}
	return c.readScreenOnce(ctx, session, tail)
}

func (c *Client) readScreenOnce(ctx context.Context, session string, tail int) (string, error) {
	proto, err := c.ensureProto(ctx)
	if err != nil {
		return "", err
	}

	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()

	c.setDeadline(ctx, conn)

	// Reading never needs a key (docs/protocol/daemon-control-socket.md section 3:
	// auth is optional for attach, and the daemon rejects only a *wrong* key, never a
	// missing one). Deliberately never fetch or send one here: a stale control.key —
	// wrong, rotated, whatever — would otherwise make the daemon reject with EAUTH an
	// attach that would have succeeded fine with no auth field at all, breaking a read
	// path that has no actual need for a credential.
	req := map[string]interface{}{
		"proto": proto,
		"op":    "attach",
		"short": session,
		"cols":  80,
		"rows":  24,
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
	// goes idle, so the explicit ceiling below is what ends the read in that case, and
	// what has been accumulated by then is a real, valid partial screen.
	//
	// The ceiling is passed explicitly, as an absolute time.Time independent of ctx's
	// own deadline, rather than relying solely on whatever deadline ctx happens to
	// carry by this point: collectUntilIdleOrClosed's own hard ceiling must not depend
	// on a caller upstream having derived one on ctx (see its own doc comment).
	data, closed := collectUntilIdleOrClosed(ctx, conn, reader, c.readIdleTimeout, maxAttachBytes, false, time.Now().Add(c.screenDeadline))

	// The kick marker means this attach connection was evicted (see detectKick's
	// comment for the full three-condition rule, and
	// docs/protocol/daemon-control-socket.md section 8). A real kick is a normal event —
	// someone attached by hand and took over — so the screen accumulated before the
	// marker is still returned alongside the typed error, rather than thrown away: the
	// caller loses nothing it would otherwise have had. It is still subject to the same
	// tail limit as the ordinary path below — the caller asked for the last tail bytes
	// either way, and a kick is common enough (see docs/protocol/daemon-control-socket.md
	// section 8) that skipping the limit here could hand back up to the full
	// maxAttachBytes instead of what was actually requested.
	if prefix, detail, kicked := detectKick(data, closed); kicked {
		return string(applyTail(prefix, tail)), &ErrKicked{Detail: detail}
	}

	return string(applyTail(data, tail)), nil
}

// applyTail trims data to at most the last tail bytes, without splitting a UTF-8 rune.
// tail <= 0 means keep everything data already holds — which is itself never more than
// maxAttachBytes, a limit collectUntilIdleOrClosed enforces regardless of tail.
func applyTail(data []byte, tail int) []byte {
	if tail > 0 && len(data) > tail {
		return trimToRuneBoundary(data[len(data)-tail:])
	}
	return data
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

	conn, err := c.dial(ctx)
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
	//
	// The window has its own explicit ceiling, independent of ctx (SendKeys never
	// derives a context deadline the way ReadScreen does — context.Background() is its
	// documented call path). Without this, a session that keeps printing more often
	// than c.readIdleTimeout resets collectUntilIdleOrClosed's sliding idle deadline on
	// every byte, and with no ctx deadline in play either, the call never returns: it
	// blocks for the whole of the session's current turn, or forever. c.screenDeadline
	// is reused here rather than a separate field, so this window and ReadScreen's own
	// cannot silently drift apart from each other.
	data, closed := collectUntilIdleOrClosed(ctx, conn, reader, c.readIdleTimeout, maxAttachBytes, true, time.Now().Add(c.screenDeadline))

	if _, detail, kicked := detectKick(data, closed); kicked {
		// The keys were written to the connection, but a kick observed right after
		// means another attacher may have taken over before (or as) the daemon
		// applied them — unlike a plain close with no marker, this is a signal worth
		// surfacing distinctly rather than folding into "success". SendKeys has no use
		// for the accumulated prefix (there is no screen to hand back on this path).
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
