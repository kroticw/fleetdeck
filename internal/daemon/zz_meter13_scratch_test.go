package daemon

// Scratch: does an attach WITHOUT the control key reshape the session, the way
// an authed one does? ReadScreen deliberately sends no auth (client.go), so the
// answer decides whether today's poller is already reshaping sessions.

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

func TestMeterWidthAuthedVsNot(t *testing.T) {
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

	dial := func(cols int, authed bool) (net.Conn, func(time.Duration) string) {
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req := map[string]any{"proto": proto, "op": "attach", "short": short, "cols": cols, "rows": 30}
		if authed {
			req["auth"] = key
		}
		line, _ := json.Marshal(req)
		_, _ = conn.Write(append(line, '\n'))
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

	wide, readWide := dial(150, true)
	defer wide.Close()
	t.Logf("A. wide(150, authed) opening:            %s", readWide(2*time.Second))

	// unauthenticated narrow attacher — exactly what ReadScreen sends
	noauth, readNoauth := dial(80, false)
	t.Logf("B. narrow(80, NO auth) opening:          %s", readNoauth(2*time.Second))
	t.Logf("C. wide(150) after unauthed narrow:      %s", readWide(2*time.Second))
	_ = noauth.Close()
	time.Sleep(1 * time.Second)
	t.Logf("D. wide(150) settle:                     %s", readWide(2*time.Second))

	// authenticated narrow attacher
	auth, readAuth := dial(80, true)
	t.Logf("E. narrow(80, authed) opening:           %s", readAuth(2*time.Second))
	t.Logf("F. wide(150) after authed narrow:        %s", readWide(2*time.Second))
	_ = auth.Close()
}
