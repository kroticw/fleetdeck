package daemon

// Scratch: does the cols/rows an attacher asks for actually resize the session?
// Observer attaches at 80x24 and watches the width of what it receives while a
// second attacher asks for 200x50.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func widestLine(s string) int {
	w := 0
	for _, line := range strings.Split(s, "\n") {
		// strip CSI sequences crudely: count printable runes only
		n := 0
		esc := false
		for _, r := range line {
			if r == 0x1b {
				esc = true
				continue
			}
			if esc {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
					esc = false
				}
				continue
			}
			if r >= 0x20 {
				n++
			}
		}
		if n > w {
			w = n
		}
	}
	return w
}

func TestMeterResizeEffect(t *testing.T) {
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

	attach := func(cols, rows int) (net.Conn, func() string) {
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": cols, "rows": rows, "auth": key})
		_, _ = conn.Write(append(req, '\n'))
		acc := []byte{}
		return conn, func() string {
			b := make([]byte, 65536)
			deadline := time.Now().Add(1500 * time.Millisecond)
			for time.Now().Before(deadline) {
				_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
				n, err := conn.Read(b)
				acc = append(acc, b[:n]...)
				if err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						break
					}
					break
				}
			}
			out := string(acc)
			acc = nil
			return out
		}
	}

	obs, readObs := attach(80, 24)
	defer obs.Close()
	first := readObs()
	t.Logf("observer(80x24) before: widest printable line = %d chars, %d bytes", widestLine(first), len(first))

	wide, readWide := attach(200, 50)
	defer wide.Close()
	w1 := readWide()
	t.Logf("wide attacher(200x50) got: widest = %d chars, %d bytes", widestLine(w1), len(w1))

	after := readObs()
	t.Logf("observer(80x24) after wide attacher: widest = %d chars, %d bytes", widestLine(after), len(after))
}
