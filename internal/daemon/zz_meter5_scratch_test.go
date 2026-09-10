package daemon

// Scratch: does a long-lived attach connection count as a client that holds the
// daemon open? Hold one for 40s; `claude daemon status` is run alongside.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func TestMeterHoldAttach(t *testing.T) {
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
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 80, "rows": 24, "auth": key})
	_, _ = conn.Write(append(req, '\n'))
	go func() {
		b := make([]byte, 65536)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			if _, err := conn.Read(b); err != nil {
				return
			}
		}
	}()
	t.Logf("attach held open, sleeping 40s (pid %d)", os.Getpid())
	time.Sleep(40 * time.Second)
	t.Logf("done")
}
