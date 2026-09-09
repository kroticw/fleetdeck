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

	// Set deadline based on context
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		// Set a reasonable timeout
		conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

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

	// Set deadline based on context
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	// Send list request with proto
	req := map[string]interface{}{
		"proto": c.proto,
		"op":    "list",
	}

	if err := c.writeRequest(conn, req); err != nil {
		return nil, err
	}

	resp, err := readResponse(conn)
	if err != nil {
		return nil, err
	}

	ok, _ := resp["ok"].(bool)
	if !ok {
		return nil, daemonError(resp)
	}

	// Parse the jobs array
	jobsData, ok := resp["jobs"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid jobs response")
	}

	var sessions []Session
	for _, jobInterface := range jobsData {
		jobJSON, err := json.Marshal(jobInterface)
		if err != nil {
			return nil, err
		}

		var session Session
		if err := json.Unmarshal(jobJSON, &session); err != nil {
			return nil, err
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}
