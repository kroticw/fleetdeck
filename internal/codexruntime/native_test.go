package codexruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/executor"
)

const nativeThread = "01a1a6d4-90a4-70f3-9130-a59ec873d0ba"
const foreignThread = "02a1a6d4-90a4-70f3-9130-a59ec873d0ba"

func nativeServer(t *testing.T, run func(context.Context, *websocket.Conn)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fleetdeck-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		init := nativeRead(ctx, t, conn)
		if init.Method != "initialize" {
			t.Errorf("first method=%s", init.Method)
			return
		}
		nativeWrite(ctx, t, conn, map[string]any{"id": init.ID, "result": map[string]any{}})
		if msg := nativeRead(ctx, t, conn); msg.Method != "initialized" {
			t.Errorf("missing initialized: %s", msg.Method)
			return
		}
		run(ctx, conn)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { cancel(); _ = server.Close(); _ = listener.Close() })
	return socket
}

func nativeRead(ctx context.Context, t *testing.T, conn *websocket.Conn) nativeMessage {
	t.Helper()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Error(err)
		return nativeMessage{}
	}
	var m nativeMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Error(err)
	}
	return m
}

func nativeWrite(ctx context.Context, t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Error(err)
		return
	}
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Error(err)
	}
}

func TestNativeControlRepliesCanArriveOutOfOrder(t *testing.T) {
	socket := nativeServer(t, func(ctx context.Context, c *websocket.Conn) {
		a, b := nativeRead(ctx, t, c), nativeRead(ctx, t, c)
		for _, m := range []nativeMessage{b, a} {
			nativeWrite(ctx, t, c, map[string]any{"id": m.ID, "result": map[string]string{"method": m.Method}})
		}
		<-ctx.Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := openNativeControl(ctx, socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	results := make(chan error, 2)
	for _, method := range []string{"thread/read", "thread/turns/list"} {
		go func() {
			var got struct {
				Method string `json:"method"`
			}
			err := c.call(ctx, method, map[string]string{"threadId": nativeThread}, &got)
			if err == nil && got.Method != method {
				err = errors.New("response routed to the wrong call")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeControlOwnsOnlyItsThreadAndPreservesRequestIDs(t *testing.T) {
	for _, id := range []json.RawMessage{json.RawMessage(`0`), json.RawMessage(`"request-0"`)} {
		t.Run(string(id), func(t *testing.T) {
			ready := make(chan struct{})
			replied := make(chan nativeMessage, 1)
			socket := nativeServer(t, func(ctx context.Context, c *websocket.Conn) {
				<-ready
				for _, thread := range []string{foreignThread, nativeThread} {
					nativeWrite(ctx, t, c, map[string]any{"id": id, "method": "item/commandExecution/requestApproval", "params": map[string]string{"threadId": thread, "turnId": "turn-1", "itemId": "item-1"}})
				}
				replied <- nativeRead(ctx, t, c)
				<-ctx.Done()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			events := make(chan nativeMessage, 2)
			c, err := openNativeControl(ctx, socket, func(m nativeMessage) { events <- m })
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.own(nativeThread)
			close(ready)
			var req nativeMessage
			select {
			case req = <-events:
			case <-ctx.Done():
				t.Fatal("missing owned approval")
			}
			if requestThread(req.Params) != nativeThread || req.Token == "" {
				t.Fatal("foreign request escaped ownership filter")
			}
			if err := c.respond(ctx, req.Token, func() {}); !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("unencodable response: %v", err)
			}
			if err := c.respond(ctx, req.Token, map[string]string{"decision": "accept"}); err != nil {
				t.Fatal(err)
			}
			select {
			case reply := <-replied:
				if string(reply.ID) != string(id) {
					t.Fatalf("reply id=%s want=%s", reply.ID, id)
				}
			case <-ctx.Done():
				t.Fatal("native reply never arrived")
			}
			if err := c.respond(ctx, req.Token, map[string]string{"decision": "accept"}); !errors.Is(err, executor.ErrConflict) {
				t.Fatalf("duplicate approval: %v", err)
			}
			select {
			case <-events:
				t.Fatal("foreign request was delivered")
			default:
			}
		})
	}
}

func TestNativeLostAckIsUnknownAndNeverRetried(t *testing.T) {
	seen := make(chan nativeMessage, 1)
	socket := nativeServer(t, func(ctx context.Context, c *websocket.Conn) { seen <- nativeRead(ctx, t, c); _ = c.CloseNow() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := openNativeControl(ctx, socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	err = c.call(ctx, "turn/start", map[string]string{"threadId": nativeThread}, nil)
	if !errors.Is(err, ErrReplyUnknown) {
		t.Fatalf("lost acknowledgment: %v", err)
	}
	if (<-seen).Method != "turn/start" {
		t.Fatal("wrong native call")
	}
}

func TestNativeReplyWithoutResultCannotConfirmAcceptance(t *testing.T) {
	socket := nativeServer(t, func(ctx context.Context, c *websocket.Conn) {
		request := nativeRead(ctx, t, c)
		nativeWrite(ctx, t, c, map[string]any{"id": request.ID})
		<-ctx.Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := openNativeControl(ctx, socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.call(ctx, "turn/start", nil, nil); err == nil {
		t.Fatal("empty reply counted as an accepted call")
	}
}

func TestNativeQueuedAnswerCannotOutliveOwnership(t *testing.T) {
	ready := make(chan struct{})
	socket := nativeServer(t, func(ctx context.Context, c *websocket.Conn) {
		<-ready
		nativeWrite(ctx, t, c, map[string]any{"id": 0, "method": "item/commandExecution/requestApproval", "params": map[string]string{"threadId": nativeThread}})
		<-ctx.Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events := make(chan nativeMessage, 1)
	c, err := openNativeControl(ctx, socket, func(m nativeMessage) { events <- m })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.own(nativeThread)
	close(ready)
	var pending nativeMessage
	select {
	case pending = <-events:
	case <-ctx.Done():
		t.Fatal("missing approval")
	}
	c.writer <- struct{}{}
	answered := make(chan error, 1)
	go func() { answered <- c.respond(ctx, pending.Token, map[string]string{"decision": "accept"}) }()
	select {
	case <-answered:
		<-c.writer
		t.Fatal("answer did not wait for the writer")
	case <-time.After(20 * time.Millisecond):
	}
	c.own(foreignThread)
	<-c.writer
	if err := <-answered; !errors.Is(err, executor.ErrConflict) {
		t.Fatalf("stale answer was sent: %v", err)
	}
}

func TestNativeWriteQueueRespectsCanceledContext(t *testing.T) {
	socket := nativeServer(t, func(ctx context.Context, _ *websocket.Conn) { <-ctx.Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := openNativeControl(ctx, socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.writer <- struct{}{}
	callCtx, stopCall := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stopCall()
	finished := make(chan error, 1)
	go func() { finished <- c.write(callCtx, nativeMessage{Method: "test"}) }()
	select {
	case err := <-finished:
		<-c.writer
		if err == nil {
			t.Fatal("expired write succeeded")
		}
	case <-time.After(200 * time.Millisecond):
		<-c.writer
		<-finished
		t.Fatal("write queue ignored its deadline")
	}
}

func TestNativeCallDeadlineDoesNotWaitForBusyOwnership(t *testing.T) {
	c := &nativeControl{writer: make(chan struct{}, 1), done: make(chan struct{}), calls: make(map[string]chan nativeMessage)}
	// Simulate an approval write holding ownership while the socket is full.
	c.writer <- struct{}{}
	c.ownerMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- c.call(ctx, "thread/read", nil, nil) }()
	select {
	case err := <-finished:
		c.ownerMu.Unlock()
		<-c.writer
		if err == nil {
			t.Fatal("expired call succeeded")
		}
	case <-time.After(200 * time.Millisecond):
		c.ownerMu.Unlock()
		<-c.writer
		<-finished
		t.Fatal("RPC call waited for a busy ownership lock after its deadline")
	}
}
