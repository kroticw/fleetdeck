package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// SocketPath returns the path to the daemon control socket for the current user.
// filepath.Glob returns candidates in lexicographic order, which is not the same as
// "most recently started daemon" — a dead socket left behind by a crashed daemon can
// sort before a live one. Try each candidate and return the first that actually
// accepts a connection.
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

	if len(matches) == 0 {
		return "", ErrDaemonUnavailable
	}

	for _, candidate := range matches {
		conn, err := net.DialTimeout("unix", candidate, 500*time.Millisecond)
		if err != nil {
			continue
		}
		conn.Close()
		return candidate, nil
	}

	return "", ErrDaemonUnavailable
}

// setDeadline sets a deadline on the connection from context, or a fallback based on the client.
func (c *Client) setDeadline(ctx context.Context, conn net.Conn) {
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(time.Duration(c.defaultDeadlineSecs) * time.Second))
	}
}

// ControlKey reads the control key from ~/.claude/daemon/control.key.
func ControlKey() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", ErrNoControlKey
	}

	keyPath := filepath.Join(homeDir, ".claude", "daemon", "control.key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", ErrNoControlKey
	}

	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", ErrNoControlKey
	}

	return key, nil
}

// Client represents a connection to the daemon control socket.
type Client struct {
	socketPath          string
	keyFunc             func() (string, error)
	protoMu             sync.Mutex    // guards proto; the client is shared between a poller and request handlers
	proto               int           // cached protocol number from ping
	readIdleTimeout     time.Duration // idle timeout for ReadScreen (default 300ms)
	defaultDeadlineSecs int           // default deadline in seconds when context has none (default 30)
}

// New creates a new daemon client.
func New(socketPath string, key func() (string, error)) *Client {
	return &Client{
		socketPath:          socketPath,
		keyFunc:             key,
		proto:               0,
		readIdleTimeout:     300 * time.Millisecond,
		defaultDeadlineSecs: 30,
	}
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

// readResponse reads and unmarshals a JSON response from the daemon.
func readResponse(conn net.Conn) (map[string]interface{}, error) {
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, err
	}

	return resp, nil
}

// readAttachHeader reads the JSON header line that opens an attach response and
// returns an error when the daemon refused the attach (e.g. EAUTH, ENOJOB).
func readAttachHeader(reader *bufio.Reader) error {
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return err
	}

	ok, _ := resp["ok"].(bool)
	if !ok {
		return daemonError(resp)
	}
	return nil
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
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return Info{}, fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	defer conn.Close()

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
	if proto, ok := resp["proto"].(float64); ok {
		info.Proto = int(proto)
		c.protoMu.Lock()
		c.proto = info.Proto
		c.protoMu.Unlock()
	}

	return info, nil
}

// ListSessions retrieves the list of active sessions.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	proto, err := c.ensureProto(ctx)
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	defer conn.Close()

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
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	var resp struct {
		Ok   bool       `json:"ok"`
		Jobs *[]Session `json:"jobs"`
		Code string     `json:"code,omitempty"`
	}

	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, err
	}

	if !resp.Ok {
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

	// Get the control key first, before making any network calls
	key, err := c.keyFunc()
	if err != nil {
		return ErrNoControlKey
	}

	proto, err := c.ensureProto(ctx)
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	defer conn.Close()

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
// sequence whose start was cut off. It guards against handing a caller a byte slice
// that begins mid-rune (which would also garble an ANSI escape sequence cut the same
// way) after we truncate a byte buffer from the front.
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
// cols/rows are fixed at 80x24 for this attach. Investigation against the live daemon
// (see branch-fix-report.md) could not conclusively prove or disprove that these values
// resize the session's real PTY; the daemon's bg-pty-host process is spawned with a
// fixed size independent of any single attach's cols/rows, which suggests the shared
// PTY is not resized per-attacher, but this was not confirmed against the resize-handling
// code itself. Treat this as an open risk, not a closed question.
func (c *Client) ReadScreen(ctx context.Context, session string, tail int) (string, error) {
	proto, err := c.ensureProto(ctx)
	if err != nil {
		return "", err
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	defer conn.Close()

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

	// Read the streamed bytes with idle detection.
	// The daemon keeps the attach connection open, so we need to detect when
	// the stream goes idle (no data for c.readIdleTimeout) and return promptly,
	// not wait for the full context deadline.
	const maxBytes = 1024 * 1024 // 1MB cap
	data := make([]byte, 0, maxBytes)

	lastReadTime := time.Now()

readLoop:
	for {
		// A session that prints continuously (a spinner, say) never goes idle, so
		// the context deadline is the only thing that ends the read. When it fires,
		// what has been accumulated so far is a real, valid partial screen — return
		// it rather than discarding it.
		if ctx.Err() != nil {
			break readLoop
		}

		// Calculate the read deadline: use the shorter of context deadline and idle timeout
		var readDeadline time.Time
		if deadline, ok := ctx.Deadline(); ok {
			readDeadline = deadline
		} else {
			// Use the defaultDeadlineSecs as the hard ceiling
			readDeadline = time.Now().Add(time.Duration(c.defaultDeadlineSecs) * time.Second)
		}

		// Also consider idle timeout: if no data arrives within readIdleTimeout, we're idle
		idleDeadline := lastReadTime.Add(c.readIdleTimeout)
		if idleDeadline.Before(readDeadline) {
			readDeadline = idleDeadline
		}

		conn.SetReadDeadline(readDeadline)

		buf := make([]byte, 4096)
		n, err := reader.Read(buf)

		if n > 0 {
			// Got data - update last read time
			lastReadTime = time.Now()
			data = append(data, buf[:n]...)
			if len(data) > maxBytes {
				data = trimToRuneBoundary(data[len(data)-maxBytes:])
			}
		}

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Read timeout occurred. Determine if it's because of idle or context deadline.
				// If we've been idle for >= readIdleTimeout, the stream is idle.
				if time.Since(lastReadTime) >= c.readIdleTimeout {
					// Stream is idle - return what we have
					break readLoop
				}
				// Otherwise the context deadline fired; loop back to the ctx.Err()
				// check above, which returns the accumulated data.
				continue
			}
			// Real error (EOF, connection closed, etc.) - stop reading
			break readLoop
		}
	}

	// Apply tail limit if needed
	if tail > 0 && len(data) > tail {
		data = trimToRuneBoundary(data[len(data)-tail:])
	}

	return string(data), nil
}

// SendKeys sends key bytes via an attach connection.
func (c *Client) SendKeys(ctx context.Context, session, keys string) error {
	proto, err := c.ensureProto(ctx)
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	defer conn.Close()

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
		return err
	}

	// Read the JSON header line; a refused attach must not look like delivered input.
	reader := bufio.NewReader(conn)
	if err := readAttachHeader(reader); err != nil {
		return err
	}

	// Write the key bytes
	if _, err := conn.Write([]byte(keys)); err != nil {
		return err
	}

	return nil
}
