package codexruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProtocolRejectsIdentityAndVersionBeforeInvokingHandler(t *testing.T) {
	for _, tt := range []struct {
		name   string
		modify func(*request)
		code   string
	}{
		{"foreign key", func(r *request) { r.Identity.Key = "codex:02a1a6d4-90a4-70f3-9130-a59ec873d0ba" }, "identity"},
		{"old nonce", func(r *request) { r.Identity.Nonce = strings.Repeat("z", 32) }, "identity"},
		{"reused PID", func(r *request) { r.Identity.PID++ }, "identity"},
		{"old process", func(r *request) { r.Identity.ProcessStartedAt = "other" }, "identity"},
		{"future version", func(r *request) { r.Version++ }, "incompatible"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, b := net.Pipe()
			defer func() { _ = b.Close() }()
			var invoked atomic.Bool
			ended := make(chan error, 1)
			go func() {
				ended <- serveConn(context.Background(), a, runtimeRecord(), func(context.Context, string, json.RawMessage) (any, error) { invoked.Store(true); return nil, nil })
			}()
			r := request{Version: ProtocolVersion, ID: "request-1", Identity: identityOf(runtimeRecord()), Method: "stop"}
			tt.modify(&r)
			if err := writeFrame(b, r); err != nil {
				t.Fatal(err)
			}
			var response response
			if err := readFrame(bufio.NewReader(b), &response); err != nil {
				t.Fatal(err)
			}
			if response.Error == nil || response.Error.Code != tt.code || invoked.Load() {
				t.Fatalf("response=%+v invoked=%v", response, invoked.Load())
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), runtimeRecord().Nonce) {
				t.Fatal("rejected caller received the current runtime nonce")
			}
			if err := <-ended; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProtocolFramesAreBoundedAndTruncationIsExplicit(t *testing.T) {
	for _, input := range []string{"{\"id\":", "{not-json}\n", strings.Repeat("x", maxFrameSize+1) + "\n"} {
		var r request
		err := readFrame(bufio.NewReader(strings.NewReader(input)), &r)
		if !errors.Is(err, ErrInvalidFrame) && !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("invalid frame accepted: %v", err)
		}
	}
}

func TestProtocolSilentConnectionRespectsContext(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- serveConn(ctx, a, runtimeRecord(), nil) }()
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("silent connection succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("silent connection exceeded its context")
	}
}

func protocolListener(t *testing.T, record Record, handle handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fleetdeck-rpc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "runtime.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { _ = serveConn(context.Background(), conn, record, handle) }()
		}
	}()
	return socket
}

func TestProtocolClientConfirmsRuntimeIdentityAndHidesHandlerErrors(t *testing.T) {
	record := runtimeRecord()
	socket := protocolListener(t, record, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		if method == "fail" {
			return nil, errors.New("private token should never leave the runtime")
		}
		return map[string]string{"state": "ready"}, nil
	})
	c := Client{Socket: socket, Record: record}
	if err := c.Call(context.Background(), "", nil, nil); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("malformed request misclassified: %v", err)
	}
	var got struct {
		State string `json:"state"`
	}
	if err := c.Call(context.Background(), "status", nil, &got); err != nil || got.State != "ready" {
		t.Fatalf("result=%+v err=%v", got, err)
	}
	if err := c.Call(context.Background(), "fail", nil, nil); !errors.Is(err, ErrRPCFailed) || strings.Contains(err.Error(), "private token") {
		t.Fatalf("handler error leaked or lost classification: %v", err)
	}
	c.Record.PID++
	if err := c.Call(context.Background(), "status", nil, &got); !errors.Is(err, ErrIdentity) {
		t.Fatalf("stale identity accepted: %v", err)
	}
}

func TestProtocolClientRejectsSymlinkSocket(t *testing.T) {
	record := runtimeRecord()
	socket := protocolListener(t, record, nil)
	link := filepath.Join(filepath.Dir(socket), "linked.sock")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	c := Client{Socket: link, Record: record}
	if err := c.Call(context.Background(), "hello", nil, nil); !errors.Is(err, ErrPrivateState) {
		t.Fatalf("symlink socket accepted: %v", err)
	}
}
