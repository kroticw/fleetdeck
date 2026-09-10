package daemon

// Scratch: proper width probe.
//
// Claude Code's TUI draws full-width horizontal rules out of U+2500 (─). The
// longest unbroken run of that character is the session's real column count,
// and it does not move when the session merely prints more output — unlike a
// byte count or a "longest line", which do.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func longestRule(s string) int {
	best, run := 0, 0
	for _, r := range s {
		if r == '─' {
			run++
			if run > best {
				best = run
			}
		} else {
			run = 0
		}
	}
	return best
}

func TestMeterWidthProbe(t *testing.T) {
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
			return sb.String()
		}
	}

	obs, readObs := attach(80, 24)
	defer obs.Close()
	t.Logf("step 1 observer(80x24) alone:            rule=%d", longestRule(readObs(1500*time.Millisecond)))

	wide, readWide := attach(200, 50)
	t.Logf("step 2 wide attacher(200x50) first draw: rule=%d", longestRule(readWide(1500*time.Millisecond)))
	t.Logf("step 2 observer(80x24) sees:             rule=%d", longestRule(readObs(1500*time.Millisecond)))

	_ = wide.Close()
	time.Sleep(500 * time.Millisecond)

	narrow, readNarrow := attach(80, 24)
	defer narrow.Close()
	t.Logf("step 3 narrow attacher(80x24) draw:      rule=%d", longestRule(readNarrow(1500*time.Millisecond)))
	t.Logf("step 3 observer(80x24) sees:             rule=%d", longestRule(readObs(1500*time.Millisecond)))
}
