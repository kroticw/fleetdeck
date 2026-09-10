package daemon

// Scratch: does the panel's own ReadScreen/SendKeys resize a session's PTY?
//
// The width is read from the kernel (stty on the session's own tty device),
// not from the byte stream. That sign does not move because someone is looking
// at it — unlike a rendered frame, which is drawn per attacher.
//
// METER_TTY  — the session's tty, e.g. /dev/ttys006 (lsof -p <worker pid>)
// METER_SHORT — the session's short id

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"time"
)

var sizeRe = regexp.MustCompile(`(\d+) rows; (\d+) columns`)

func ttySize(t *testing.T) (rows, cols int) {
	t.Helper()
	dev := os.Getenv("METER_TTY")
	cmd := exec.Command("/bin/sh", "-c", "stty -a < "+dev)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("stty: %v (%s)", err, out)
	}
	m := sizeRe.FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("no size in stty output: %s", out)
	}
	rows, _ = strconv.Atoi(m[1])
	cols, _ = strconv.Atoi(m[2])
	return
}

func TestMeterKernelWidthEffect(t *testing.T) {
	short := os.Getenv("METER_SHORT")
	if short == "" || os.Getenv("METER_TTY") == "" {
		t.Skip("set METER_SHORT and METER_TTY")
	}
	c := liveClient(t)

	r, w := ttySize(t)
	t.Logf("0. baseline, kernel says:                     %dx%d", w, r)

	// exactly what the screen tab does once a second
	res := c.ReadScreen(context.Background(), short, 64<<10)
	time.Sleep(500 * time.Millisecond)
	r, w = ttySize(t)
	t.Logf("1. after one ReadScreen (asks 80x24):         %dx%d   (err=%v)", w, r, res.Err)

	for i := 0; i < 3; i++ {
		_ = c.ReadScreen(context.Background(), short, 64<<10)
		time.Sleep(300 * time.Millisecond)
	}
	r, w = ttySize(t)
	t.Logf("2. after three more ReadScreens:              %dx%d", w, r)

	// exactly what a key button does
	if err := c.SendKeys(context.Background(), short, ""); err != nil {
		t.Logf("   SendKeys err: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	r, w = ttySize(t)
	t.Logf("3. after one SendKeys (asks 80x24):           %dx%d", w, r)

	// CONTROL: a probe that is supposed to move the width. If this does not
	// move it either, the meter is blind and steps 1-3 prove nothing.
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 137, "rows": 41, "auth": key})
	_, _ = conn.Write(append(req, '\n'))
	go func() {
		b := make([]byte, 65536)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
			if _, err := conn.Read(b); err != nil {
				return
			}
		}
	}()
	time.Sleep(1500 * time.Millisecond)
	r, w = ttySize(t)
	t.Logf("4. CONTROL: after attach asking 137x41:       %dx%d", w, r)

	// and now the panel again, while that viewer is still attached
	_ = c.ReadScreen(context.Background(), short, 64<<10)
	time.Sleep(500 * time.Millisecond)
	r, w = ttySize(t)
	t.Logf("5. panel ReadScreen while 137-viewer sits:    %dx%d", w, r)

	_ = conn.Close()
	time.Sleep(1 * time.Second)
	r, w = ttySize(t)
	t.Logf("6. after the 137-viewer left:                 %dx%d", w, r)
}
