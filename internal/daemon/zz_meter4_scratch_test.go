package daemon

// Scratch end-to-end latency probe.
//
// One marker is written into a session's PTY. Two observers race for it:
//
//	A) a live attach stream held open from before the write  -> t_pty
//	B) the panel's own HTTP endpoint, polled as fast as it answers -> t_http
//
// t_pty measures what the daemon actually costs to deliver a keystroke's echo.
// t_http measures what the panel's snapshot mechanism costs on top of that.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMeterEndToEndMarker(t *testing.T) {
	short := os.Getenv("METER_SHORT")
	base := os.Getenv("METER_PANEL")
	if short == "" || base == "" {
		t.Skip("set METER_SHORT and METER_PANEL")
	}
	c := liveClient(t)
	proto, err := c.ensureProto(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key, _ := c.keyFunc()
	marker := fmt.Sprintf("ZQX%d", time.Now().UnixNano()%1000000)

	// A: observer attach, opened first and left open.
	obs, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()
	req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 80, "rows": 24, "auth": key})
	_, _ = obs.Write(append(req, '\n'))

	ptyHit := make(chan time.Duration, 1)
	var t0 time.Time
	go func() {
		acc := make([]byte, 0, 1<<20)
		b := make([]byte, 65536)
		for {
			_ = obs.SetReadDeadline(time.Now().Add(15 * time.Second))
			n, err := obs.Read(b)
			acc = append(acc, b[:n]...)
			if !t0.IsZero() && strings.Contains(string(acc), marker) {
				ptyHit <- time.Since(t0)
				return
			}
			if err != nil {
				ptyHit <- -1
				return
			}
		}
	}()
	time.Sleep(700 * time.Millisecond) // let the initial repaint drain

	// B: HTTP observer.
	httpHit := make(chan time.Duration, 1)
	go func() {
		for {
			if !t0.IsZero() && time.Since(t0) > 20*time.Second {
				httpHit <- -1
				return
			}
			resp, err := http.Get(base + "/api/sessions/" + short + "/screen")
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !t0.IsZero() && strings.Contains(string(body), marker) {
				httpHit <- time.Since(t0)
				return
			}
		}
	}()
	time.Sleep(500 * time.Millisecond)

	// The write. SendKeys is exactly what the panel's key buttons use.
	t0 = time.Now()
	if err := c.SendKeys(context.Background(), short, marker); err != nil {
		t.Logf("SendKeys: %v", err)
	}
	tSend := time.Since(t0)

	var pty, htt time.Duration
	select {
	case pty = <-ptyHit:
	case <-time.After(20 * time.Second):
		pty = -2
	}
	select {
	case htt = <-httpHit:
	case <-time.After(25 * time.Second):
		htt = -2
	}
	t.Logf("E2E %s marker=%s sendKeysReturned=%s  t_pty(live attach)=%s  t_http(/screen)=%s  overhead=%s",
		short, marker, rd(tSend), rd(pty), rd(htt), rd(htt-pty))

	// clean the marker out of the prompt box
	_ = c.SendKeys(context.Background(), short, strings.Repeat("", len(marker)))
}
