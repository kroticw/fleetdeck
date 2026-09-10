package daemon

// Scratch: what rule governs the shared width? A staircase of requested widths,
// with one observer watching what it actually gets served.

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

func TestMeterWidthStaircase(t *testing.T) {
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

	dial := func(cols, rows int) (net.Conn, func(time.Duration) string) {
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": cols, "rows": rows, "auth": key})
		_, _ = conn.Write(append(req, '\n'))
		return conn, func(d time.Duration) string {
			var sb strings.Builder
			b := make([]byte, 65536)
			deadline := time.Now().Add(d)
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
			if len(s) == 0 {
				return "SILENT"
			}
			return fmt.Sprintf("rule=%d (%d bytes)", longestRule(s), len(s))
		}
	}

	obs, readObs := dial(100, 30)
	defer obs.Close()
	t.Logf("observer(100) opening draw:  %s", readObs(2*time.Second))

	for _, w := range []int{160, 90, 200, 70} {
		conn, read := dial(w, 30)
		t.Logf("  probe(%3d) own draw:       %s", w, read(2*time.Second))
		t.Logf("  observer(100) then sees:   %s", readObs(2*time.Second))
		_ = conn.Close()
		time.Sleep(400 * time.Millisecond)
	}
}
