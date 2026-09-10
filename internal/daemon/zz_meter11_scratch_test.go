package daemon

// Scratch: is there a floor on how narrow a frame the TUI will draw?

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMeterNarrowFloor(t *testing.T) {
	short := os.Getenv("METER_SHORT")
	if short == "" {
		t.Skip("set METER_SHORT")
	}
	c := liveClient(t)
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()

	for _, w := range []int{40, 60, 80, 90, 100, 110, 130} {
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": w, "rows": 30, "auth": key})
		_, _ = conn.Write(append(req, '\n'))
		var sb strings.Builder
		b := make([]byte, 65536)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
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
		msg := "SILENT"
		if len(s) > 0 {
			msg = fmt.Sprintf("rule=%d (%d bytes)", longestRule(s), len(s))
		}
		t.Logf("asked cols=%3d -> drawn %s", w, msg)
		_ = conn.Close()
		time.Sleep(400 * time.Millisecond)
	}
}
