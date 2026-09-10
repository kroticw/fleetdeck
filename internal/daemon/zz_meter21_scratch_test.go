package daemon

// Scratch: capture a frame at the session's own wide geometry, without the
// panel's 80-column attach touching it, and dump it for the browser probe.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMeterCaptureWideFrame(t *testing.T) {
	short := os.Getenv("METER_SHORT")
	out := os.Getenv("METER_OUT")
	if short == "" || out == "" {
		t.Skip("set METER_SHORT and METER_OUT")
	}
	c := liveClient(t)
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 190, "rows": 45, "auth": key})
	_, _ = conn.Write(append(req, '\n'))

	var sb strings.Builder
	b := make([]byte, 65536)
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, err := conn.Read(b)
		sb.Write(b[:n])
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}
	}
	s := sb.String()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:] // drop the JSON header line
	}
	enc, _ := json.Marshal(s)
	if err := os.WriteFile(out, enc, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d bytes of frame, longest rule = %d", len(s), longestRule(s))
}
