package daemon

// Scratch measurement harness for the "no terminal / real pty" research run.
// Not part of the product; deleted before anything is committed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Control case: a fake daemon whose attach stream we control exactly.
// idle  -> writes one screenful and then goes quiet
// busy  -> keeps writing a byte every 50ms forever (a redrawing spinner)
// ---------------------------------------------------------------------------

func fakeDaemon(t *testing.T, busy bool) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "control.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				var req map[string]any
				_ = json.Unmarshal(trimLine(buf[:n]), &req)
				switch req["op"] {
				case "ping":
					fmt.Fprintf(c, "{\"ok\":true,\"op\":\"ping\",\"version\":\"fake\",\"proto\":1}\n")
				case "attach":
					fmt.Fprintf(c, "{\"ok\":true,\"op\":\"attach\"}\n")
					_, _ = c.Write([]byte("\x1b[2Jhello screen\r\n"))
					if busy {
						for {
							if _, err := c.Write([]byte(".")); err != nil {
								return
							}
							time.Sleep(50 * time.Millisecond)
						}
					}
					// idle: hold the connection open, silent, like a real
					// daemon does for a session that is not redrawing.
					time.Sleep(30 * time.Second)
				default:
					fmt.Fprintf(c, "{\"ok\":false,\"op\":\"?\"}\n")
				}
			}(conn)
		}
	}()
	return sock
}

func trimLine(b []byte) []byte {
	for i, c := range b {
		if c == '\n' {
			return b[:i]
		}
	}
	return b
}

func TestMeterControlIdleVsBusy(t *testing.T) {
	for _, tc := range []struct {
		name string
		busy bool
	}{{"idle", false}, {"busy", true}} {
		sock := fakeDaemon(t, tc.busy)
		c := New(sock, func() (string, error) { return "0123456789abcdef0123456789abcdef", nil })
		var samples []time.Duration
		for i := 0; i < 5; i++ {
			start := time.Now()
			res := c.ReadScreen(context.Background(), "fake", 200)
			samples = append(samples, time.Since(start))
			if res.Err != nil {
				t.Logf("  (err: %v)", res.Err)
			}
		}
		report(t, "CONTROL/"+tc.name, samples)
	}
}

// ---------------------------------------------------------------------------
// Live case: the real daemon on this machine.
// ---------------------------------------------------------------------------

func liveClient(t *testing.T) *Client {
	t.Helper()
	path, err := SocketPath()
	if err != nil {
		t.Skipf("no live daemon: %v", err)
	}
	t.Logf("socket: %s", path)
	return New(path, func() (string, error) {
		b, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".claude", "daemon", "control.key"))
		if err != nil {
			return "", err
		}
		return string(b), nil
	})
}

func TestMeterLiveReadScreen(t *testing.T) {
	c := liveClient(t)
	sessions, err := c.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	only := os.Getenv("METER_SHORT")
	n := 5
	if v := os.Getenv("METER_N"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}
	for _, s := range sessions {
		if only != "" && s.Short != only {
			continue
		}
		var samples []time.Duration
		var bytesSeen []int
		for i := 0; i < n; i++ {
			start := time.Now()
			res := c.ReadScreen(context.Background(), s.Short, 200)
			samples = append(samples, time.Since(start))
			bytesSeen = append(bytesSeen, len(res.Screen))
			if res.Err != nil {
				t.Logf("  %s err: %v", s.Short, res.Err)
			}
			time.Sleep(200 * time.Millisecond)
		}
		report(t, fmt.Sprintf("LIVE/%s state=%s tempo=%s bytes=%v", s.Short, s.State, s.Tempo, bytesSeen), samples)
	}
}

// TestMeterLiveBreakdown splits one ReadScreen into its phases.
func TestMeterLiveBreakdown(t *testing.T) {
	c := liveClient(t)
	short := os.Getenv("METER_SHORT")
	if short == "" {
		sessions, err := c.ListSessions(context.Background())
		if err != nil || len(sessions) == 0 {
			t.Skip("no sessions")
		}
		short = sessions[0].Short
	}
	for i := 0; i < 5; i++ {
		t0 := time.Now()
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		tDial := time.Since(t0)

		t1 := time.Now()
		proto, err := c.ensureProto(context.Background())
		if err != nil {
			t.Logf("proto: %v", err)
		}
		tPing := time.Since(t1)

		key, _ := c.keyFunc()
		req := map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 80, "rows": 24, "auth": key}
		line, _ := json.Marshal(req)
		t2 := time.Now()
		_, _ = conn.Write(append(line, '\n'))
		hdr := make([]byte, 0, 4096)
		buf := make([]byte, 1)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			n, err := conn.Read(buf)
			if err != nil || n == 0 {
				break
			}
			if buf[0] == '\n' {
				break
			}
			hdr = append(hdr, buf[0])
		}
		tHeader := time.Since(t2)

		// first PTY byte after the header
		t3 := time.Now()
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		fb := make([]byte, 1)
		_, ferr := conn.Read(fb)
		tFirstByte := time.Since(t3)

		// how long until the stream goes quiet for 300ms
		t4 := time.Now()
		total := 1
		quiet := false
		for {
			_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			b := make([]byte, 65536)
			n, err := conn.Read(b)
			total += n
			if err != nil {
				quiet = true
				break
			}
			if time.Since(t4) > 4*time.Second {
				break
			}
		}
		tDrain := time.Since(t4)
		_ = conn.Close()

		t.Logf("BREAKDOWN %s #%d dial=%s ping=%s attachHeader=%s firstPtyByte=%s(err=%v) drainToIdle=%s quiet=%v bytes=%d",
			short, i, rd(tDial), rd(tPing), rd(tHeader), rd(tFirstByte), ferr, rd(tDrain), quiet, total)
		time.Sleep(300 * time.Millisecond)
	}
}

// TestMeterAttachExclusivity checks whether a second attach evicts the first.
func TestMeterAttachExclusivity(t *testing.T) {
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
	open := func(tag string) (net.Conn, chan string) {
		conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"proto": proto, "op": "attach", "short": short, "cols": 80, "rows": 24, "auth": key})
		_, _ = conn.Write(append(req, '\n'))
		out := make(chan string, 1)
		go func() {
			acc := make([]byte, 0, 1<<20)
			b := make([]byte, 65536)
			for {
				_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
				n, err := conn.Read(b)
				acc = append(acc, b[:n]...)
				if err != nil {
					out <- fmt.Sprintf("%s: closed after %d bytes, err=%v, tail=%q", tag, len(acc), err, tailOf(acc, 120))
					return
				}
			}
		}()
		return conn, out
	}
	c1, out1 := open("A")
	defer c1.Close()
	time.Sleep(1 * time.Second)
	c2, out2 := open("B")
	defer c2.Close()
	select {
	case msg := <-out1:
		t.Logf("FIRST ATTACHER %s", msg)
	case <-time.After(6 * time.Second):
		t.Logf("FIRST ATTACHER A: still open 5s after B attached — NOT evicted")
	}
	select {
	case msg := <-out2:
		t.Logf("SECOND ATTACHER %s", msg)
	case <-time.After(2 * time.Second):
		t.Logf("SECOND ATTACHER B: still open")
	}
}

func tailOf(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

func rd(d time.Duration) string { return d.Round(time.Millisecond).String() }

func report(t *testing.T, tag string, s []time.Duration) {
	t.Helper()
	sorted := append([]time.Duration(nil), s...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var sum time.Duration
	for _, d := range s {
		sum += d
	}
	t.Logf("%-46s n=%d min=%s med=%s max=%s mean=%s  raw=%v",
		tag, len(s), rd(sorted[0]), rd(sorted[len(sorted)/2]), rd(sorted[len(sorted)-1]), rd(sum/time.Duration(len(s))), s)
}
