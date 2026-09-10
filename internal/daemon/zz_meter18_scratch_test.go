package daemon

// Scratch: what rule sets the shared pty size? Read from the kernel (stty), not
// from rendered frames — a frame is drawn per attacher and lies about this.
//
// Hypothesis under test: size = max over currently attached viewers.

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func holdAttach(t *testing.T, c *Client, proto int, key, short string, cols, rows int) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": cols, "rows": rows, "auth": key})
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
	time.Sleep(1200 * time.Millisecond)
	return conn
}

func TestMeterWidthRule(t *testing.T) {
	short := os.Getenv("METER_SHORT")
	if short == "" || os.Getenv("METER_TTY") == "" {
		t.Skip("set METER_SHORT and METER_TTY")
	}
	c := liveClient(t)
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()
	say := func(label string) {
		r, w := ttySize(t)
		t.Logf("%-52s %dx%d", label, w, r)
	}

	say("0. fresh session:")

	big := holdAttach(t, c, proto, key, short, 150, 40)
	say("1. viewer(150) attached:")

	small := holdAttach(t, c, proto, key, short, 80, 24)
	say("2. viewer(80) joins while 150 is attached:")

	_ = c.ReadScreen(context.Background(), short, 64<<10)
	time.Sleep(500 * time.Millisecond)
	say("3. panel ReadScreen(80) with both attached:")

	_ = big.Close()
	time.Sleep(1500 * time.Millisecond)
	say("4. the 150 viewer leaves, 80 stays:")

	_ = small.Close()
	time.Sleep(1500 * time.Millisecond)
	say("5. everyone left:")

	_ = c.ReadScreen(context.Background(), short, 64<<10)
	time.Sleep(500 * time.Millisecond)
	say("6. panel ReadScreen(80) alone:")

	wide := holdAttach(t, c, proto, key, short, 190, 45)
	say("7. viewer(190) attaches:")
	_ = c.ReadScreen(context.Background(), short, 64<<10)
	time.Sleep(500 * time.Millisecond)
	say("8. panel ReadScreen(80) with 190 attached:")
	_ = wide.Close()
	time.Sleep(1500 * time.Millisecond)
	say("9. after 190 leaves:")
}
