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
)

// maxLineBytes bounds how much of a single line — a response, or the attach header —
// this client will buffer before giving up. It is not modelled on the daemon's own 1MB
// request cap (see ETOOLARGE): that number bounds what the daemon accepts as input, not
// what a response can grow to, and `list` is exactly the shape that does not fit that
// mould — its response grows with the size of the fleet, and each session's `intent`
// field carries the last prompt submitted to it, which can itself run to kilobytes. A
// cap sized for a request would make ListSessions fail outright, with no degradation,
// against a fleet with enough sessions or long enough prompts. This is instead a plain
// resource bound: buffering more than this many bytes for one line, from either side of
// the connection, is treated as a malformed or hostile peer rather than a legitimate
// answer, with enough headroom that an ordinary fleet's `list` response is nowhere near
// it.
const maxLineBytes = 16 * 1024 * 1024

// socketGlobBase is the directory SocketPath globs candidates under. It is a
// package-level var, not a literal inlined at the call site, purely so a test can
// point it at a temporary directory and exercise SocketPath's own glob-then-resolve
// logic deterministically — without a live daemon, and without skipping in CI, unlike
// a test driven against the real, uid-specific /tmp path (see
// TestSocketPathFindsLiveDaemonSocket, which necessarily skips when this machine has
// no live daemon).
var socketGlobBase = "/tmp"

// SocketPath returns the path to the daemon control socket for the current user.
// filepath.Glob returns candidates in lexicographic order, which is not the same as
// "most recently started daemon" — a dead socket left behind by a crashed daemon can
// sort before a live one. Try each candidate and return the first that is both safely
// owned and actually accepts a connection.
//
// The ordering matters for skipping a dead candidate; it is not a deliberate choice
// among several live ones. Exactly one live daemon per uid is the expected shape in
// production — see resolveSocketCandidate's own comment for what happens on the rarer
// path where more than one is actually live at once.
func SocketPath() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	pattern := filepath.Join(socketGlobBase, fmt.Sprintf("cc-daemon-%s", currentUser.Uid), "*", "control.sock")
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
//
// When more than one candidate is both safely owned and actually live at the same
// time — several daemon processes running for the same uid, which is not the expected
// shape but is not prevented by anything this function checks — the one returned is
// simply the first in lexicographic order, not the newest or otherwise most
// significant one. This function makes no attempt to distinguish that case from the
// single-live-daemon case; the choice among several live daemons is arbitrary.
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

	// Every iteration of the loop above either returned success or appended a cause,
	// and matches is non-empty (checked above), so causes always holds at least one
	// entry by the time the loop ends without returning — there is no third outcome
	// left to handle here.
	return "", errors.Join(append([]error{ErrDaemonUnavailable}, causes...)...)
}

// Sentinel causes for a socket-ownership refusal. Wrapping these with %w into the
// human-readable messages built below lets a caller (or a test) assert on the specific
// reason via errors.Is, without binding to the exact wording, which stays free to
// change — see checkKeyFileSecurity's own sentinels for the symmetric client-side
// check on the key file.
var (
	errSocketSymlink          = errors.New("refusing a symlinked socket path")
	errSocketNotASocket       = errors.New("socket path does not refer to a Unix domain socket")
	errSocketWrongOwner       = errors.New("socket path is owned by a different user")
	errSocketWritableByOthers = errors.New("socket path is writable by group or other")
	errSocketMissingStickyBit = errors.New("root-owned ancestor is writable by group or other and missing the sticky bit")
	errSocketTooManyAncestors = errors.New("too many parent directories to check")
)

// checkSocketOwnership verifies that the socket file is owned by the current user, and
// that it and every enclosing directory up to (but not including) the first one owned
// by root is not writable by group or other. The "not writable by group or other" half
// of that applies to the enclosing directories, not to the socket file itself — see the
// comment beside its own ownership check for why.
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
	return checkSocketOwnershipUID(socketPath, os.Getuid())
}

// checkSocketOwnershipUID is checkSocketOwnership with the expected uid taken as a
// parameter rather than read from os.Getuid(). wantUID is a parameter purely so a test
// can exercise the socket-file-itself ownership mismatch (errSocketWrongOwner)
// deterministically, by passing a wantUID that cannot match any real file's owner,
// without needing a second real user account on the machine running the test.
func checkSocketOwnershipUID(socketPath string, wantUID int) error {
	sockStat, err := realLstat(socketPath)
	if err != nil {
		return fmt.Errorf("checking socket path %s: %w", socketPath, err)
	}
	if sockStat.symlink {
		return fmt.Errorf("refusing socket %s: %w", socketPath, errSocketSymlink)
	}
	// Defence in depth, not a hole this closes: net.Dial("unix", ...) against a
	// non-socket path fails on its own regardless. But realLstat already has
	// info.Mode() in hand, and every other property this function checks is checked
	// explicitly rather than assumed — the path actually being a socket should be no
	// different.
	if sockStat.mode&os.ModeSocket == 0 {
		return fmt.Errorf("refusing socket %s: %w", socketPath, errSocketNotASocket)
	}
	if sockStat.uid != wantUID {
		return fmt.Errorf("refusing socket %s: %w", socketPath, errSocketWrongOwner)
	}
	// The socket file's own mode bits are deliberately not checked for group/other
	// writability, unlike the enclosing directories below. Planting or replacing a
	// directory entry — including this socket file — requires write access to the
	// directory that contains it, not to the file itself, and that directory's own
	// mode is exactly what checkSocketOwnershipWalk checks next; a permissive mode on
	// the socket file in isolation gives a local attacker nothing they could not
	// already do by controlling the directory. Checking it anyway used to produce a
	// false refusal that was hard to diagnose: a daemon started under a permissive
	// umask (e.g. 002) creates the socket as srwxrwxr-x, which is perfectly safe by
	// the reasoning above but was refused here as "writable by group or other" all the
	// same.
	return checkSocketOwnershipWalk(socketPath, filepath.Dir(socketPath), wantUID, realLstat)
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
				return fmt.Errorf("refusing socket %s: %s is a symlink owned by a non-root user: %w", socketPath, path, errSocketSymlink)
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
				return fmt.Errorf("refusing socket %s: %s: %w", socketPath, path, errSocketMissingStickyBit)
			}
			// A root-owned enclosing directory (e.g. /private/tmp, the real target of
			// macOS's /tmp symlink) that is either not writable by group/other, or is
			// and carries the sticky bit, is outside a local attacker's control.
			// Nothing further up needs checking.
			return nil
		}

		if info.uid != uid {
			return fmt.Errorf("refusing socket %s: %s: %w", socketPath, path, errSocketWrongOwner)
		}
		if info.mode&0o022 != 0 {
			return fmt.Errorf("refusing socket %s: %s: %w", socketPath, path, errSocketWritableByOthers)
		}

		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}

	return fmt.Errorf("refusing socket %s: %w", socketPath, errSocketTooManyAncestors)
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

// Sentinel causes for a control-key-file refusal, wrapped by ControlKey into
// ErrNoControlKey (see wrapNoControlKey) but still reachable via errors.Is/As for
// diagnosis, symmetric with checkSocketOwnership's own sentinels above.
var (
	errKeyFileSymlink      = errors.New("control key file is a symlink, refusing to use it")
	errKeyFileUnknownOwner = errors.New("cannot determine the owner of the control key file")
	errKeyFileWrongOwner   = errors.New("control key file is owned by a different user")
	errKeyFileInsecureMode = errors.New("control key file is readable or writable by group or other")
	errKeyDirSymlink       = errors.New("control key directory is a symlink, refusing to use it")
	errKeyDirUnknownOwner  = errors.New("cannot determine the owner of the control key directory")
	errKeyDirWrongOwner    = errors.New("control key directory is owned by a different user")
	errKeyDirInsecureMode  = errors.New("control key directory is readable, writable, or searchable by group or other")
)

// checkKeyFileSecurity refuses a control key file that is not owned by wantUID, or is
// readable or writable by group or other, and applies the same two checks to the
// file's immediate containing directory — section 6 of the protocol document requires
// that directory to be mode 0700, owned by the user, same as the key file itself.
// This is symmetric with checkSocketOwnership's own check on the socket path, except
// it stops at the one containing directory section 6 actually names rather than
// walking every ancestor up to a root-owned boundary: unlike /tmp, the key file's
// parent directories above ~/.claude/daemon are not a shared, world-writable location
// a local attacker could plant something under. wantUID is a parameter, rather than
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
		return errKeyFileSymlink
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errKeyFileUnknownOwner
	}

	if int(stat.Uid) != wantUID {
		return errKeyFileWrongOwner
	}
	if !keyFileModeIsSecure(info.Mode()) {
		return errKeyFileInsecureMode
	}

	return checkKeyDirSecurity(filepath.Dir(path), wantUID)
}

// checkKeyDirSecurity refuses a control key directory that is not owned by wantUID, or
// is readable, writable, or searchable by group or other — the 0700 section 6 of the
// protocol document requires. It is the directory half of checkKeyFileSecurity, not a
// general-purpose ancestor walk: a symlinked directory is refused outright rather than
// resolved, since (unlike /tmp on macOS) there is no legitimate production shape in
// which ~/.claude/daemon is itself a symlink.
func checkKeyDirSecurity(dir string, wantUID int) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return errKeyDirSymlink
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errKeyDirUnknownOwner
	}

	if int(stat.Uid) != wantUID {
		return errKeyDirWrongOwner
	}
	if !keyFileModeIsSecure(info.Mode()) {
		return errKeyDirInsecureMode
	}
	return nil
}

// keyFileModeIsSecure reports whether mode denies every group and other permission
// bit. It is shared by the key file check (0600) and the containing directory check
// (0700): both are the same rule — no group or other bits at all — just applied to
// different modes; see docs/protocol/daemon-control-socket.md section 6.
func keyFileModeIsSecure(mode os.FileMode) bool {
	return mode&0o077 == 0
}

// stubKeyFunc is substituted for a nil key function passed to New, so every call site
// that needs a key gets a clean ErrNoControlKey instead of a nil-pointer panic. Reading
// (ListSessions, Ping, and an attach that only reads) never needs a key at all, so it is
// unaffected.
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
	defaultDeadline time.Duration // default deadline when context carries none (default 30s)

	// How long Resume waits for a dispatched worker, and how often it asks.
	// Fields rather than constants purely so the tests can shorten them: a
	// test of the timeout that actually waited thirty seconds would be
	// skipped by whoever ran the suite next. Their real values, and why they
	// are those, are on resume.go's own constants.
	resumeTimeout    time.Duration
	resumeSettle     time.Duration
	resumePoll       time.Duration
	resumeRetryDelay time.Duration
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
		socketPath:       socketPath,
		keyFunc:          key,
		defaultDeadline:  30 * time.Second,
		resumeTimeout:    resumeTimeout,
		resumeSettle:     resumeSettle,
		resumePoll:       resumePoll,
		resumeRetryDelay: resumeRetryDelay,
	}
}

// Discover creates a client that resolves its socket path via SocketPath and
// re-resolves it exactly once whenever a dial fails, so a daemon restart under a new
// socket directory — its directory name is not stable across restarts — is recovered
// from automatically instead of leaving the client stuck with ErrDaemonUnavailable
// until the process holding it is itself restarted.
//
// Discover never fails because no daemon happens to be running yet: this is the
// client meant to survive a daemon that is not up at construction time (a process
// started before the daemon, or racing its startup), not just one that restarts later.
// It resolves eagerly when a socket already exists, purely so a caller gets an
// immediately-usable path in the common case, but a failure to do so here is not
// reported — it is left for dial's own re-resolution to pick up on first use,
// identically to how a later restart is handled. There is consequently no path through
// this function that can fail, which is why it returns *Client alone rather than
// (*Client, error): a signature that always returns a nil error just moves the "did
// this fail" question to every call site instead of answering it here, in the one
// place that actually knows the answer.
func Discover(key func() (string, error)) *Client {
	c := New("", key)
	c.discoverable = true
	c.resolve = SocketPath
	if path, err := SocketPath(); err == nil {
		c.socketPath = path
	}
	return c
}

// dialTimeout bounds the connect(2) call itself, independently of whatever deadline
// (if any) ctx carries. context.Background() is the documented call path for
// ListSessions, SendText and Ping, so a Dialer with no Timeout of its own left the
// connect phase completely unbounded on every one of those paths: a daemon that is alive but not
// accepting connections (a full backlog, a wedged process) can make connect(2) on an
// AF_UNIX socket block indefinitely, and nothing could break a caller's goroutine out
// of that. resolveSocketCandidate already dials its liveness probes with
// net.DialTimeout for exactly this reason; dialTimeout is the same defence carried to
// the main connect path.
const dialTimeout = 5 * time.Second

// newControlDialer builds the *net.Dialer dialChecked connects with. It is a
// package-level function value, not a literal inlined at the call site, purely so a
// test can substitute it to observe how dialChecked configures the Dialer it actually
// uses — there is no local fixture that reproduces a genuine connect(2) hang on a
// unix socket to assert against (see dialTimeout's comment), so this seam is what lets
// TestDialCheckedActuallyUsesConfiguredDialer catch a regression that removes the
// Timeout here, which the fixture-based approach cannot.
var newControlDialer = func() *net.Dialer {
	return &net.Dialer{Timeout: dialTimeout}
}

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
	// A dial failure caused by ctx already being done (context.Canceled,
	// context.DeadlineExceeded) is not evidence the socket path is stale — the caller
	// has simply given up. Re-resolving anyway would still spend up to c.resolve's own
	// fixed budget (500ms per candidate; see below) chasing a socket for a request
	// that can no longer use the answer.
	if ctx.Err() != nil {
		return nil, err
	}
	if !c.discoverable || c.resolve == nil {
		return nil, err
	}

	// c.resolve (SocketPath, for a Discover-created client) takes no context: it dials
	// each glob candidate with its own fixed 500ms timeout (see resolveSocketCandidate)
	// and cannot be cancelled early by ctx. This is deliberate, not an oversight:
	// SocketPath's own signature is `func() (string, error)`, shared by every test that
	// stubs c.resolve directly (`client.resolve = func() (string, error) { ... }`), and
	// threading a context through would mean changing that signature and every one of
	// those call sites for a bound that already exists in a different, coarser form —
	// at most a small, fixed number of candidates (in practice one or two) at 500ms
	// each, never unbounded. A caller with a very short ctx deadline can still see this
	// call outlast it by up to that fixed amount; that is the accepted trade-off.
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
	conn, err := newControlDialer().DialContext(ctx, "unix", path)
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
// maxLineBytes before one is found — see maxLineBytes' own comment for why that bound
// is sized as buffering headroom rather than a mirror of the daemon's request cap — so
// a malformed or hostile peer cannot make the client hold an unbounded line in memory.
func readBoundedLine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, 256)
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		// Checked before appending, not after: appending first and checking
		// afterward would let a line's own terminating '\n' slip through one byte
		// past the cap, since the very next branch returns success as soon as it
		// sees that byte.
		if len(line) >= maxLineBytes {
			return nil, fmt.Errorf("line exceeds %d bytes", maxLineBytes)
		}
		line = append(line, b)
		if b == '\n' {
			return line, nil
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

// kickSearchWindow bounds how far back from the end of the accumulated stream
// findKickOpener scans for the kick marker's last occurrence. This is a PERFORMANCE
// bound, not a correctness condition: it exists only so bytes.LastIndex does not walk
// a whole read chunk (up to attachReadChunk) backwards on every read of a held attach.
//
// It contributes nothing to correctness because maxKickReasonBytes's length check
// already excludes everything this bound would: a marker at offset pos leaves a
// "reason" of len(data)-pos-len(ekickedPrefix) bytes for parseKickedMarker to accept,
// and that check already rejects anything longer than maxKickReasonBytes (256). Since
// maxKickReasonBytes+len(ekickedPrefix) (264) is smaller than kickSearchWindow (512),
// any position this window would exclude (further than 512 bytes from the end) is
// already further than 264 bytes from the end, and so is already rejected by the
// length check regardless of whether the search ever looks there. An earlier version
// of this comment, and of docs/protocol/daemon-control-socket.md
// section 8, claimed this window was a third, independently necessary correctness
// condition alongside "connection closed" and "reason looks like a reason" — that
// claim was false, demonstrated false by the argument above, and has been removed;
// the real rule has two conditions, not three (see parseKickedMarker).
const kickSearchWindow = 512

// maxKickReasonBytes bounds how long the text following the marker may be and still
// pass as the daemon's own short, human-readable reason. See the second condition in
// parseKickedMarker's comment.
const maxKickReasonBytes = 256

// findKickOpener returns the offset of the kick marker's last occurrence within
// kickSearchWindow bytes of the end of data, or -1 if there is none there. The window
// is a performance bound only (see kickSearchWindow's comment) — it never changes the
// answer parseKickedMarker ultimately reaches, only how much of data must be scanned to
// reach it.
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

// parseKickedMarker decides, once an attach connection has closed, whether it was
// kicked, given the bytes still held back when it closed. On a real kick it returns the
// prefix — everything before the marker, the screen accumulated up to that point — and
// the reason text, trimmed of surrounding whitespace.
//
// Per docs/protocol/daemon-control-socket.md section 8, detection must never fire on the
// marker's mere presence, or even on its *last occurrence* alone — only on it being the
// *last thing sent*, immediately before a close. Two conditions are required together;
// each is individually insufficient:
//
//  1. The connection actually closed. This condition is the caller's: Attachment.Read
//     calls this only when a read fails. A live session's attach stays open indefinitely
//     and may display the literal marker text as part of its own screen (see
//     TestAttachMarkerOnAnOpenStreamIsNotAKickUntilItCloses and
//     TestAttachMarkerShownOnScreenIsNotHeldBack). Without it, any screen containing the
//     text at all would be misreported as a kick.
//  2. What follows the marker's last occurrence is short and contains no newline. The
//     daemon's real reason text is the last thing it sends, flush against whatever PTY
//     bytes were already in flight — never anchored to offset 0 or a line boundary, so
//     anchoring the search there would miss it (TestAttachKickFlushAgainstTheScreenIsFound).
//     A screen that merely *displays* the marker almost always has more rendered content
//     after it, typically containing at least one newline, or is simply too long to be a
//     reason (TestAttachMarkerShownOnScreenIsNotAKick, TestAttachLongTextAfterTheMarkerIsNotAKick).
//
// The held attach applies condition 2 twice: kickHoldback while the stream is open,
// deciding what to hold back, and this function at the close, on what was held. So
// through Attach, what reaches this function has always passed kickHoldback first, and
// neither copy can be seen failing there while the other stands. Each is pinned on its
// own (TestParseKickedMarkerRules,
// TestKickHoldbackHoldsACompleteMarkerOnlyWhileItCouldBeAReason).
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

// SendText sends text into a session via the reply operation. The `reply` operation
// (docs/protocol/daemon-control-socket.md section 3) always delivers and submits the
// text — there is no way to place text in a session's prompt without sending it — so
// this has exactly one legal calling convention, unlike an earlier version that took a
// submit bool whose only legal value was true.
func (c *Client) SendText(ctx context.Context, session, text string) error {
	err := c.sendTextOnce(ctx, session, text)
	if isProtoErr(err) {
		// This retry cannot double-deliver the text: sendTextOnce writes the whole
		// request — proto field and text together — in a single write (see
		// writeRequest), and the daemon checks proto before it ever executes `reply`
		// (docs/protocol/daemon-control-socket.md section 2). An EPROTO response
		// therefore always means the text was never delivered at all. Retrying here is
		// safe for that reason.
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
