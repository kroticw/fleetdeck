package daemon

// Scratch: sharpened width probe. Distinguishes "the observer saw a frame N
// columns wide" from "the observer saw nothing at all in this window", so a
// silent window is never read as a width of zero.

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

func TestMeterWidthProbe2(t *testing.T) {
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

	attach := func(cols, rows int) (net.Conn, func(time.Duration) string) {
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

	a, readA := attach(80, 24)
	defer a.Close()
	_ = readA(2 * time.Second) // drain the initial repaint
	t.Logf("1. A(80) alone, second window:      %s", readA(2*time.Second))

	b, readB := attach(120, 30)
	t.Logf("2. B(120) own first draw:           %s", readB(2*time.Second))
	t.Logf("3. A(80) while B(120) attached:     %s", readA(2*time.Second))
	t.Logf("4. A(80) again, still with B:       %s", readA(2*time.Second))

	_ = b.Close()
	t.Logf("5. A(80) after B closed:            %s", readA(3*time.Second))
	t.Logf("6. A(80) once more:                 %s", readA(3*time.Second))
}
