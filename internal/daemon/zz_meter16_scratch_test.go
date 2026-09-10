package daemon

// Scratch: is cols/rows required on attach, and does omitting it leave the
// session's geometry alone? Also: does the daemon tell a client the session's
// current geometry anywhere (attach header, list record)?

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"
)

func attachRaw(t *testing.T, c *Client, proto int, req map[string]any, hold time.Duration) string {
	t.Helper()
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(req)
	_, _ = conn.Write(append(line, '\n'))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	hdr, err := r.ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return "READ HEADER FAILED: " + err.Error()
	}
	if hold > 0 {
		go func() {
			b := make([]byte, 65536)
			for {
				_ = conn.SetReadDeadline(time.Now().Add(hold))
				if _, err := conn.Read(b); err != nil {
					return
				}
			}
		}()
		time.Sleep(hold)
	}
	_ = conn.Close()
	return hdr
}

func TestMeterAttachWithoutGeometry(t *testing.T) {
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

	// Put the session at a distinctive size first, so any drift is visible.
	_ = attachRaw(t, c, proto, map[string]any{
		"proto": proto, "op": "attach", "short": short, "cols": 111, "rows": 33, "auth": key,
	}, 1200*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	r, w := ttySize(t)
	t.Logf("0. set to 111x33, kernel says:                %dx%d", w, r)

	// A: no cols/rows at all, no auth (what a read path could send).
	hdr := attachRaw(t, c, proto, map[string]any{
		"proto": proto, "op": "attach", "short": short,
	}, 1200*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	r, w = ttySize(t)
	t.Logf("A. attach with NO cols/rows, no auth:         %dx%d", w, r)
	t.Logf("   header: %s", trunc(hdr, 400))

	// B: no cols/rows, with auth.
	hdr = attachRaw(t, c, proto, map[string]any{
		"proto": proto, "op": "attach", "short": short, "auth": key,
	}, 1200*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	r, w = ttySize(t)
	t.Logf("B. attach with NO cols/rows, with auth:       %dx%d", w, r)

	// C: cols/rows present but zero.
	hdr = attachRaw(t, c, proto, map[string]any{
		"proto": proto, "op": "attach", "short": short, "cols": 0, "rows": 0, "auth": key,
	}, 1200*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	r, w = ttySize(t)
	t.Logf("C. attach with cols=0 rows=0:                 %dx%d", w, r)
	t.Logf("   header: %s", trunc(hdr, 400))

	// D: does `list` carry the geometry anywhere?
	sessions, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, s := range sessions {
		if s.Short == short {
			raw, _ := json.Marshal(s)
			t.Logf("D. list record for this session: %s", trunc(string(raw), 600))
		}
	}
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
