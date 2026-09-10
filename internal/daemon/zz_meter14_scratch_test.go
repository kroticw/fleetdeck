package daemon

// Scratch: does the panel's snapshot path (ReadScreen, cols=80, no auth) take
// the width back from a viewer sitting at 150?

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func TestMeterPanelTakesWidthBack(t *testing.T) {
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

	r0 := c.ReadScreen(context.Background(), short, 64<<10)
	t.Logf("1. ReadScreen alone:                       rule=%d", longestRule(r0.Screen))

	// a viewer at 150, held open, like an operator's wide terminal
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 150, "rows": 40, "auth": key})
	_, _ = conn.Write(append(req, '\n'))
	go func() {
		b := make([]byte, 65536)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			if _, err := conn.Read(b); err != nil {
				return
			}
		}
	}()
	time.Sleep(1500 * time.Millisecond)

	for i := 1; i <= 4; i++ {
		r := c.ReadScreen(context.Background(), short, 64<<10)
		t.Logf("%d. ReadScreen with 150-viewer attached:    rule=%d", i+1, longestRule(r.Screen))
		time.Sleep(700 * time.Millisecond)
	}
}
