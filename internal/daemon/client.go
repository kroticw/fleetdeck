package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// SocketPath returns the path to the daemon control socket for the current user.
// Returns the first match found; when multiple matches exist, order is undefined.
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

	return matches[0], nil
}

// setDeadline sets a deadline on the connection from context, or a 30-second fallback.
func setDeadline(ctx context.Context, conn net.Conn) {
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(30 * time.Second))
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
	socketPath string
	keyFunc    func() (string, error)
	proto      int // cached protocol number from ping
}

// New creates a new daemon client.
func New(socketPath string, key func() (string, error)) *Client {
	return &Client{
		socketPath: socketPath,
		keyFunc:    key,
		proto:      0,
	}
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

// daemonError converts a daemon error response into a typed error.
func daemonError(resp map[string]interface{}) error {
	code, ok := resp["code"].(string)
	if !ok {
		return fmt.Errorf("unknown error")
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
		return Info{}, ErrDaemonUnavailable
	}
	defer conn.Close()

	setDeadline(ctx, conn)

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
		c.proto = int(proto)
		info.Proto = c.proto
	}

	return info, nil
}

// ListSessions retrieves the list of active sessions.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	// If proto is not cached, ping first
	if c.proto == 0 {
		if _, err := c.Ping(ctx); err != nil {
			return nil, err
		}
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return nil, ErrDaemonUnavailable
	}
	defer conn.Close()

	setDeadline(ctx, conn)

	// Send list request with proto
	req := map[string]interface{}{
		"proto": c.proto,
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
		Ok   bool      `json:"ok"`
		Jobs []Session `json:"jobs"`
		Code string    `json:"code,omitempty"`
	}

	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, err
	}

	if !resp.Ok {
		// Reconstruct error response for daemonError
		errResp := map[string]interface{}{"code": resp.Code}
		return nil, daemonError(errResp)
	}

	return resp.Jobs, nil
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

	// If proto is not cached, ping first
	if c.proto == 0 {
		if _, err := c.Ping(ctx); err != nil {
			return err
		}
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return ErrDaemonUnavailable
	}
	defer conn.Close()

	setDeadline(ctx, conn)

	// Send reply request with proto and auth
	req := map[string]interface{}{
		"proto": c.proto,
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

// ReadScreen sends an attach request and reads the terminal stream.
// It returns the raw bytes that follow the JSON header line.
// tail limits how much of the tail to keep; tail <= 0 means keep everything.
func (c *Client) ReadScreen(ctx context.Context, session string, tail int) (string, error) {
	// If proto is not cached, ping first
	if c.proto == 0 {
		if _, err := c.Ping(ctx); err != nil {
			return "", err
		}
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return "", ErrDaemonUnavailable
	}
	defer conn.Close()

	setDeadline(ctx, conn)

	// Get control key if available
	key, _ := c.keyFunc()

	// Build attach request. Only include auth if key is available.
	req := map[string]interface{}{
		"proto": c.proto,
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

	// Read the JSON header line
	reader := bufio.NewReader(conn)
	_, err = reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	// Read the streamed bytes from the buffered reader until the connection closes or we reach a cap
	const maxBytes = 1024 * 1024 // 1MB cap
	data := make([]byte, 0, maxBytes)

	for {
		buf := make([]byte, 4096)
		n, err := reader.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
			if len(data) > maxBytes {
				data = data[len(data)-maxBytes:]
			}
		}
		if err != nil {
			break
		}
	}

	// Apply tail limit if needed
	if tail > 0 && len(data) > tail {
		data = data[len(data)-tail:]
	}

	return string(data), nil
}

// SendKeys sends key bytes via an attach connection.
func (c *Client) SendKeys(ctx context.Context, session, keys string) error {
	// If proto is not cached, ping first
	if c.proto == 0 {
		if _, err := c.Ping(ctx); err != nil {
			return err
		}
	}

	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return ErrDaemonUnavailable
	}
	defer conn.Close()

	setDeadline(ctx, conn)

	// Get control key if available
	key, _ := c.keyFunc()

	// Build attach request. Only include auth if key is available.
	req := map[string]interface{}{
		"proto": c.proto,
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

	// Read the JSON header line
	reader := bufio.NewReader(conn)
	_, err = reader.ReadString('\n')
	if err != nil {
		return err
	}

	// Write the key bytes
	if _, err := conn.Write([]byte(keys)); err != nil {
		return err
	}

	return nil
}
