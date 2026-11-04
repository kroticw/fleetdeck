package codexruntime

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

const maxFrameSize = 1 << 20
const handshakeTimeout = 5 * time.Second
const rpcTimeout = 30 * time.Second

var (
	ErrIdentity      = errors.New("runtime identity does not match saved state")
	ErrInvalidFrame  = errors.New("invalid runtime protocol frame")
	ErrFrameTooLarge = errors.New("runtime protocol frame exceeds limit")
	ErrRPCFailed     = errors.New("runtime operation failed")
	ErrReplyUnknown  = errors.New("runtime reply lost after request write")
)

type identity struct {
	Key              executor.Key `json:"key"`
	Nonce            string       `json:"nonce"`
	PID              int          `json:"pid"`
	ProcessStartedAt string       `json:"processStartedAt,omitempty"`
}

func identityOf(r Record) identity {
	return identity{Key: r.Key, Nonce: r.Nonce, PID: r.PID, ProcessStartedAt: r.ProcessStartedAt}
}

func (a identity) matches(b identity) bool {
	return a.Key == b.Key && a.PID == b.PID && a.ProcessStartedAt == b.ProcessStartedAt && subtle.ConstantTimeCompare([]byte(a.Nonce), []byte(b.Nonce)) == 1
}

type request struct {
	Version  int             `json:"version"`
	ID       string          `json:"id"`
	Identity identity        `json:"identity"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
}

type wireError struct {
	Code string `json:"code"`
}

type response struct {
	Version  int             `json:"version"`
	ID       string          `json:"id"`
	Identity *identity       `json:"identity,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *wireError      `json:"error,omitempty"`
}

// Version 1 is the first shipped private protocol, so it has no predecessor.
// A future compatible revision can widen this range explicitly.
func compatibleVersion(version int) bool { return version == ProtocolVersion }

func readFrame(reader *bufio.Reader, target any) error {
	var body []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(body)+len(part) > maxFrameSize {
			return ErrFrameTooLarge
		}
		body = append(body, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil || json.Unmarshal(body, target) != nil {
			return ErrInvalidFrame
		}
		return nil
	}
}

func writeFrame(writer io.Writer, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return ErrInvalidFrame
	}
	if len(body)+1 > maxFrameSize {
		return ErrFrameTooLarge
	}
	body = append(body, '\n')
	n, err := writer.Write(body)
	if err == nil && n != len(body) {
		return io.ErrShortWrite
	}
	return err
}

func deadline(ctx context.Context, bound time.Duration) time.Time {
	at := time.Now().Add(bound)
	if d, ok := ctx.Deadline(); ok && d.Before(at) {
		return d
	}
	return at
}

type handler func(context.Context, string, json.RawMessage) (any, error)

// serveConn handles one request. Cancellation closes only this connection,
// never the runtime or the process it owns.
func serveConn(ctx context.Context, conn net.Conn, record Record, handle handler) error {
	defer func() { _ = conn.Close() }()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if err := conn.SetDeadline(deadline(ctx, handshakeTimeout)); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	var req request
	if err := readFrame(reader, &req); err != nil {
		return err
	}
	var result any
	expected := identityOf(record)
	res := response{Version: ProtocolVersion, ID: req.ID}
	switch {
	case !compatibleVersion(req.Version):
		res.Error = &wireError{Code: "incompatible"}
	case !req.Identity.matches(expected):
		res.Error = &wireError{Code: "identity"}
	default:
		res.Identity = &expected
		if len(req.ID) == 0 || len(req.ID) > 128 || len(req.Method) == 0 || len(req.Method) > 80 {
			res.Error = &wireError{Code: "invalid"}
			break
		}
		callCtx, cancel := context.WithDeadline(ctx, deadline(ctx, rpcTimeout))
		defer cancel()
		if err := conn.SetDeadline(deadline(callCtx, rpcTimeout)); err != nil {
			return err
		}
		var err error
		if req.Method == "hello" {
			result = struct {
				Version int `json:"version"`
			}{ProtocolVersion}
		} else if handle == nil {
			err = executor.ErrUnsupported
		} else {
			result, err = handle(callCtx, req.Method, req.Params)
		}
		if err != nil {
			res.Error = &wireError{Code: errorCode(err)}
		} else {
			res.Result, err = json.Marshal(result)
			if err != nil {
				res.Result = nil
				res.Error = &wireError{Code: "internal"}
			}
		}
	}
	st, streaming := result.(streamer)
	if streaming && res.Error != nil {
		_ = st.Close()
		streaming = false
	}
	if err := writeFrame(conn, res); err != nil {
		if streaming {
			_ = st.Close()
		}
		if !errors.Is(err, ErrFrameTooLarge) {
			return err
		}
		res.Result, res.Error = nil, &wireError{Code: "too_large"}
		return writeFrame(conn, res)
	}
	if !streaming {
		return nil
	}
	// The stream outlives the RPC deadline; it ends with either side.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = st.Close()
		return err
	}
	return st.stream(ctx, conn, reader)
}

// Client addresses one saved runtime identity. It never signals a PID or
// starts a replacement merely because a socket cannot be reached.
type Client struct {
	Socket string
	Record Record
}

func (c Client) Call(ctx context.Context, method string, params, result any) error {
	conn, _, err := c.open(ctx, method, params, result)
	if err != nil {
		return err
	}
	return conn.Close()
}

// open performs one verified request and returns its connection, still
// bound to ctx, for a method whose reply is followed by a stream.
func (c Client) open(ctx context.Context, method string, params, result any) (net.Conn, *bufio.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !filepath.IsAbs(c.Socket) {
		return nil, nil, ErrPrivateState
	}
	parent, err := os.Lstat(filepath.Dir(c.Socket))
	if err != nil {
		return nil, nil, err
	}
	if !privateFile(parent, true) {
		return nil, nil, ErrPrivateState
	}
	info, err := os.Lstat(c.Socket)
	if err != nil {
		return nil, nil, err
	}
	if !privateSocket(info) {
		return nil, nil, ErrPrivateState
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, nil, ErrInvalidFrame
	}
	req := request{Version: ProtocolVersion, ID: rand.Text(), Identity: identityOf(c.Record), Method: method, Params: encoded}
	// Encode and bound the request before making any connection.
	if raw, err := json.Marshal(req); err != nil || len(raw)+1 > maxFrameSize {
		return nil, nil, ErrFrameTooLarge
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, nil, executor.ErrUnavailable
	}
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if err := conn.SetDeadline(deadline(ctx, rpcTimeout)); err != nil {
		return nil, nil, err
	}
	if err := writeFrame(conn, req); err != nil {
		return nil, nil, ErrReplyUnknown
	}
	reader := bufio.NewReader(conn)
	var res response
	if err := readFrame(reader, &res); err != nil {
		return nil, nil, ErrReplyUnknown
	}
	if res.ID != req.ID {
		return nil, nil, ErrIdentity
	}
	if res.Error != nil && (res.Error.Code == "identity" || res.Error.Code == "incompatible") {
		return nil, nil, errorFromCode(res.Error.Code)
	}
	if res.Identity == nil || !req.Identity.matches(*res.Identity) {
		return nil, nil, ErrIdentity
	}
	if res.Error != nil {
		return nil, nil, errorFromCode(res.Error.Code)
	}
	if !compatibleVersion(res.Version) {
		return nil, nil, ErrIncompatible
	}
	if result != nil && json.Unmarshal(res.Result, result) != nil {
		return nil, nil, ErrInvalidFrame
	}
	ok = true
	return conn, reader, nil
}

func errorCode(err error) string {
	for code, target := range protocolErrors {
		if errors.Is(err, target) {
			return code
		}
	}
	return "internal"
}

func errorFromCode(code string) error {
	if err, ok := protocolErrors[code]; ok {
		return err
	}
	return ErrRPCFailed
}

var protocolErrors = map[string]error{
	"identity": ErrIdentity, "incompatible": ErrIncompatible, "invalid": ErrInvalidFrame,
	"too_large": ErrFrameTooLarge, "unavailable": executor.ErrUnavailable,
	"unsupported": executor.ErrUnsupported, "conflict": executor.ErrConflict,
	"starting": executor.ErrStarting, "delivery_unknown": executor.ErrDeliveryUnknown,
	"not_resumable": executor.ErrNotResumable,
}
