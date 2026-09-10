package daemon

// Scratch: does the panel's own ReadScreen (which hardcodes 80x24) reshape a
// session that some other viewer had set wider?
//
// internal/daemon/client.go's comment above ReadScreen says it cannot. This
// puts a session at 140 columns, calls ReadScreen once, and asks an observer
// what width it is being served afterwards.

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

func TestMeterReadScreenReshapes(t *testing.T) {
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

	// A viewer at 140 columns, like an operator with a wide terminal.
	wide, readWide := attach(140, 40)
	defer wide.Close()
	t.Logf("1. wide viewer(140) first draw:            %s", readWide(2*time.Second))

	// The panel does exactly this, once a second, on the screen tab.
	res := c.ReadScreen(context.Background(), short, 64<<10)
	t.Logf("2. panel ReadScreen(80x24) returned:       rule=%d (%d bytes) err=%v",
		longestRule(res.Screen), len(res.Screen), res.Err)

	t.Logf("3. wide viewer(140) after that ReadScreen: %s", readWide(3*time.Second))
	t.Logf("4. wide viewer(140) once more:             %s", readWide(3*time.Second))
}
