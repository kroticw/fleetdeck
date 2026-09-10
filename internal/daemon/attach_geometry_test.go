package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

// recordAttachRequests answers ping and attach, and hands every attach request
// it received back to the test. The attach connection is answered with a header
// and then held open for the duration of the test, the way the real daemon holds
// it: both callers end on their idle timeout, and SendKeys writes its key bytes
// into the connection after the header.
func recordAttachRequests(t *testing.T) (socket string, requests func() []map[string]any) {
	t.Helper()

	listener, err := net.Listen("unix", tempSocket(t))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	seen := make(chan map[string]any, 8)
	// The attach connection is held open, the way the real daemon holds it: both
	// callers end their read on the idle timeout, and SendKeys writes its bytes
	// after the header, which a closed connection would refuse.
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadString('\n')
				if err != nil {
					return
				}
				var req map[string]any
				if json.Unmarshal([]byte(line), &req) != nil {
					return
				}
				switch req["op"] {
				case "ping":
					fmt.Fprint(c, `{"ok":true,"op":"ping","version":"test","proto":1}`+"\n")
				case "attach":
					seen <- req
					fmt.Fprint(c, `{"ok":true,"op":"attach"}`+"\n")
					_, _ = c.Write([]byte("screen"))
					<-stop
				}
			}(conn)
		}
	}()

	return listener.Addr().String(), func() []map[string]any {
		var out []map[string]any
		for {
			select {
			case req := <-seen:
				out = append(out, req)
			default:
				return out
			}
		}
	}
}

// TestAttachGeometryComesFromOnePlace pins the two attach-opening paths to a
// single source for the geometry they send.
//
// The geometry is not decoration: the daemon applies it to the session's PTY,
// for every viewer of that session at once (see attachResizesSessionToCols). Two
// literals mean the panel can end up asking for one size when it reads and
// another when it types, resizing the session back and forth on its own — and
// nothing outside this test would notice, because both paths look correct
// read on their own.
func TestAttachGeometryComesFromOnePlace(t *testing.T) {
	socket, requests := recordAttachRequests(t)
	client := New(socket, func() (string, error) { return "key", nil })
	// Both paths end on the idle timeout; shortening it keeps this test fast without
	// changing which request bytes go out, which is all it inspects.
	client.readIdleTimeout = 20 * time.Millisecond

	if res := client.ReadScreen(context.Background(), "abc", 1024); res.Err != nil {
		t.Fatalf("ReadScreen: %v", res.Err)
	}
	if err := client.SendKeys(context.Background(), "abc", "x"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	got := requests()
	if len(got) != 2 {
		t.Fatalf("expected 2 attach requests (one per path), got %d", len(got))
	}
	for i, req := range got {
		cols, colsOK := req["cols"].(float64)
		rows, rowsOK := req["rows"].(float64)
		if !colsOK || !rowsOK {
			t.Fatalf("request %d carries no cols/rows: %v — the daemon refuses such an attach outright with \"malformed request: Invalid input\"", i, req)
		}
		if int(cols) != attachResizesSessionToCols || int(rows) != attachResizesSessionToRows {
			t.Errorf("request %d asked for %dx%d, want %dx%d — every attach this client opens must ask for the one geometry, or reading and typing will resize the session against each other",
				i, int(cols), int(rows), attachResizesSessionToCols, attachResizesSessionToRows)
		}
	}
}

// TestAttachGeometryIsNeverZero guards the shape the daemon actually rejects.
//
// cols/rows are not optional and zero is not a way to say "leave it alone":
// an attach sent without them, or with zeros, is answered
// {"ok":false,"error":"malformed request: Invalid input","code":"EUNKNOWN"}
// and no screen comes back at all. Verified against the live daemon of CLI
// 2.1.263. So a future attempt to stop resizing sessions by zeroing this out
// would not be a smaller footprint — it would turn the screen tab off.
func TestAttachGeometryIsNeverZero(t *testing.T) {
	if attachResizesSessionToCols <= 0 || attachResizesSessionToRows <= 0 {
		t.Fatalf("geometry is %dx%d; the daemon rejects an attach whose cols/rows are missing or zero",
			attachResizesSessionToCols, attachResizesSessionToRows)
	}
}
