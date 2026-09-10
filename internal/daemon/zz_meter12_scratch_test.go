package daemon

// Scratch: with two attachers at once, whose width wins?

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

func TestMeterWidthWhoWins(t *testing.T) {
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

	dial := func(cols int) (net.Conn, func(time.Duration) string) {
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": cols, "rows": 30, "auth": key})
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

	// case 1: wide viewer first, narrow panel second
	wide, readWide := dial(150)
	t.Logf("case1 wide(150) opening:      %s", readWide(2*time.Second))
	narrow, readNarrow := dial(80)
	t.Logf("case1 narrow(80) opening:     %s", readNarrow(2*time.Second))
	t.Logf("case1 wide(150) then sees:    %s", readWide(2*time.Second))
	_ = narrow.Close()
	_ = wide.Close()
	time.Sleep(1 * time.Second)

	// case 2: narrow panel first, wide viewer second
	n2, readN2 := dial(80)
	defer n2.Close()
	t.Logf("case2 narrow(80) opening:     %s", readN2(2*time.Second))
	w2, readW2 := dial(150)
	defer w2.Close()
	t.Logf("case2 wide(150) opening:      %s", readW2(2*time.Second))
	t.Logf("case2 narrow(80) then sees:   %s", readN2(2*time.Second))
}
