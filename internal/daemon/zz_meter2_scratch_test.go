package daemon

// Scratch: do two simultaneous attachers both receive PTY bytes?

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func TestMeterAttachBroadcast(t *testing.T) {
	c := liveClient(t)
	short := os.Getenv("METER_SHORT")
	if short == "" {
		t.Skip("set METER_SHORT")
	}
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()
	type res struct {
		tag   string
		bytes int
		first time.Duration
	}
	out := make(chan res, 2)
	open := func(tag string) net.Conn {
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 80, "rows": 24, "auth": key})
		_, _ = conn.Write(append(req, '\n'))
		go func() {
			start := time.Now()
			var first time.Duration
			total := 0
			b := make([]byte, 65536)
			hdrDone := false
			for time.Since(start) < 4*time.Second {
				_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				n, err := conn.Read(b)
				if n > 0 {
					if !hdrDone {
						hdrDone = true
						continue
					}
					if first == 0 {
						first = time.Since(start)
					}
					total += n
				}
				if err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					break
				}
			}
			out <- res{tag, total, first}
		}()
		return conn
	}
	a := open("A")
	defer a.Close()
	time.Sleep(1500 * time.Millisecond)
	b := open("B")
	defer b.Close()
	for i := 0; i < 2; i++ {
		r := <-out
		t.Logf("ATTACHER %s: ptyBytesAfterHeader=%d firstByteAfterHeader=%s", r.tag, r.bytes, rd(r.first))
	}
}
