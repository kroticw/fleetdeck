package codexruntime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/executor"
)

// nativeMessage is local to the runtime. Native IDs and raw RPC payloads are
// never browser command parameters; pending requests get an opaque Token.
type nativeMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *nativeRPCError `json:"error,omitempty"`
	Token  string          `json:"-"`
}

type nativeRPCError struct {
	Code int `json:"code"`
}

func (e *nativeRPCError) Error() string {
	return fmt.Sprintf("codex RPC rejected the request (code %d)", e.Code)
}
func (e *nativeRPCError) Is(target error) bool {
	return e.Code == -32601 && target == executor.ErrUnsupported
}

type nativePending struct {
	message nativeMessage
	sending bool
}

// nativeControl owns one persistent connection. onMessage runs on its reader
// goroutine: it may update memory, but must not block or call back into RPC.
type nativeControl struct {
	conn      *websocket.Conn
	transport *http.Transport
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	writer    chan struct{}
	callsMu   sync.Mutex
	ownerMu   sync.Mutex
	prefix    string
	next      uint64
	thread    string
	calls     map[string]chan nativeMessage
	requests  map[string]nativePending
	onMessage func(nativeMessage)
}

func openNativeControl(parent context.Context, socket string, onMessage func(nativeMessage)) (*nativeControl, error) {
	resolved, err := nativeSocket(socket)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", resolved)
	}}
	initCtx, cancelInit := context.WithTimeout(parent, handshakeTimeout)
	defer cancelInit()
	conn, _, err := websocket.Dial(initCtx, "ws://localhost/", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, executor.ErrUnavailable
	}
	conn.SetReadLimit(8 << 20)
	ctx, cancel := context.WithCancel(parent)
	c := &nativeControl{conn: conn, transport: transport, ctx: ctx, cancel: cancel, done: make(chan struct{}), writer: make(chan struct{}, 1), prefix: rand.Text(), calls: make(map[string]chan nativeMessage), requests: make(map[string]nativePending), onMessage: onMessage}
	go c.read()
	err = c.call(initCtx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "fleetdeck", "version": "0.1.0"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil)
	if err == nil {
		err = c.write(initCtx, nativeMessage{Method: "initialized"})
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// The native control entry is a legitimate symlink created by Codex. Unlike
// Fleetdeck's own IPC socket it is resolved, then checked at its private target.
func nativeSocket(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrPrivateState
	}
	entryParent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	if !privateFile(entryParent, true) {
		return "", ErrPrivateState
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	parent, err := os.Lstat(filepath.Dir(resolved))
	if err != nil {
		return "", err
	}
	if !privateFile(parent, true) {
		return "", ErrPrivateState
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return "", err
	}
	if !privateSocket(info) {
		return "", ErrPrivateState
	}
	return resolved, nil
}

func (c *nativeControl) Close() {
	c.cancel()
	_ = c.conn.CloseNow()
	c.transport.CloseIdleConnections()
}

func (c *nativeControl) own(thread string) {
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	if c.thread != thread {
		c.requests = make(map[string]nativePending)
	}
	c.thread = thread
}

func encodeNative(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalidFrame
	}
	if len(raw) > maxFrameSize {
		return nil, ErrFrameTooLarge
	}
	return raw, nil
}

func (c *nativeControl) acquireWriter(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.writer <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-c.writer
			return err
		}
		select {
		case <-c.done:
			<-c.writer
			return executor.ErrUnavailable
		default:
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return executor.ErrUnavailable
	}
}

func (c *nativeControl) write(ctx context.Context, value any) error {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	raw, err := encodeNative(value)
	if err != nil {
		return err
	}
	if err := c.acquireWriter(ctx); err != nil {
		return err
	}
	defer func() { <-c.writer }()
	return c.conn.Write(ctx, websocket.MessageText, raw)
}

func (c *nativeControl) call(ctx context.Context, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return ErrInvalidFrame
	}
	c.callsMu.Lock()
	if len(c.calls) >= 64 {
		c.callsMu.Unlock()
		return executor.ErrConflict
	}
	c.next++
	id := "fleetdeck:" + c.prefix + ":" + strconv.FormatUint(c.next, 10)
	rawID, _ := json.Marshal(id)
	key, _ := nativeID(rawID)
	reply := make(chan nativeMessage, 1)
	c.calls[key] = reply
	c.callsMu.Unlock()
	defer func() { c.callsMu.Lock(); delete(c.calls, key); c.callsMu.Unlock() }()
	select {
	case <-c.done:
		return executor.ErrUnavailable
	default:
	}
	if err := c.write(ctx, nativeMessage{ID: rawID, Method: method, Params: encoded}); err != nil {
		if errors.Is(err, ErrInvalidFrame) || errors.Is(err, ErrFrameTooLarge) {
			return err
		}
		return ErrReplyUnknown
	}
	var message nativeMessage
	select {
	case message = <-reply:
	case <-c.done:
		select {
		case message = <-reply:
		default:
			return ErrReplyUnknown
		}
	case <-ctx.Done():
		return ErrReplyUnknown
	}
	if message.Error != nil {
		if len(message.Result) != 0 {
			return ErrInvalidFrame
		}
		return message.Error
	}
	if len(message.Result) == 0 {
		return ErrInvalidFrame
	}
	if result != nil && json.Unmarshal(message.Result, result) != nil {
		return ErrInvalidFrame
	}
	return nil
}

func (c *nativeControl) read() {
	defer close(c.done)
	defer c.Close()
	for {
		typ, raw, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			return
		}
		var m nativeMessage
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		if m.Method != "" {
			if len(m.Params) > maxFrameSize || !c.dispatch(m) {
				return
			}
			continue
		}
		id, ok := nativeID(m.ID)
		if !ok {
			return
		}
		c.callsMu.Lock()
		reply := c.calls[id]
		c.callsMu.Unlock()
		if reply != nil {
			select {
			case reply <- m:
			default:
			}
		}
	}
}

func (c *nativeControl) dispatch(m nativeMessage) bool {
	thread := requestThread(m.Params)
	c.ownerMu.Lock()
	owned := c.thread != "" && thread == c.thread
	global := m.Method == "account/rateLimits/updated" && len(m.ID) == 0
	if !owned && !global {
		c.ownerMu.Unlock()
		return true
	}
	if len(m.ID) > 0 {
		if _, ok := nativeID(m.ID); !ok || len(c.requests) >= 128 {
			c.ownerMu.Unlock()
			return false
		}
		m.Token = rand.Text()
		c.requests[m.Token] = nativePending{message: m}
	}
	if m.Method == "serverRequest/resolved" {
		var resolved struct {
			ID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(m.Params, &resolved) == nil {
			id, _ := nativeID(resolved.ID)
			for token, pending := range c.requests {
				if candidate, _ := nativeID(pending.message.ID); candidate == id {
					delete(c.requests, token)
				}
			}
		}
	}
	c.ownerMu.Unlock()
	if c.onMessage != nil {
		c.onMessage(m)
	}
	return true
}

// respond writes one answer, without claiming the server has acted on it.
// A lost connection requires replay/reconciliation, never an automatic retry.
func (c *nativeControl) respond(ctx context.Context, token string, result any) error {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return ErrInvalidFrame
	}
	if err := c.acquireWriter(ctx); err != nil {
		return err
	}
	defer func() { <-c.writer }()
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	pending, ok := c.requests[token]
	if !ok || pending.sending || requestThread(pending.message.Params) != c.thread {
		return executor.ErrConflict
	}
	raw, err := encodeNative(nativeMessage{ID: pending.message.ID, Result: resultJSON})
	if err != nil {
		return err
	}
	// Ownership/resolution cannot change between this validation and the
	// write. Socket writes are bounded and do not await a native RPC reply.
	pending.sending = true
	c.requests[token] = pending
	if err := c.conn.Write(ctx, websocket.MessageText, raw); err != nil {
		return ErrReplyUnknown
	}
	delete(c.requests, token)
	return nil
}

func requestThread(raw json.RawMessage) string {
	var p struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	if p.ThreadID != "" && p.Thread.ID != "" && p.ThreadID != p.Thread.ID {
		return ""
	}
	if p.ThreadID != "" {
		return p.ThreadID
	}
	return p.Thread.ID
}

// Numeric zero and the string "0" are distinct IDs. Normalizing their JSON
// spelling permits equivalent escaped strings without treating null as an ID.
func nativeID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || len(raw) > 1024 {
		return "", false
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return "", false
		}
		return "s:" + id, true
	}
	id, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return "", false
	}
	return "n:" + strconv.FormatUint(id, 10), true
}
