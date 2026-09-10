package daemon

// Scratch control case for the exclusivity probe: a fake daemon that DOES kick
// the first attacher. If the probe reports "evicted" here and "not evicted"
// against the real daemon, the probe can tell the two apart.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMeterExclusivityProbeControl(t *testing.T) {
	sock := filepath.Join("/tmp/fdmeter", "ctl.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	conns := make(chan net.Conn, 8)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 4096)
				n, _ := c.Read(buf)
				var req map[string]any
				_ = json.Unmarshal(trimLine(buf[:n]), &req)
				if req["op"] != "attach" {
					fmt.Fprintf(c, "{\"ok\":true,\"op\":\"ping\",\"version\":\"fake\",\"proto\":1}\n")
					c.Close()
					return
				}
				// kick everyone already attached, the way a Windows daemon does
				for {
					select {
					case old := <-conns:
						_, _ = old.Write([]byte("EKICKED: Session opened in another window"))
						_ = old.Close()
						continue
					default:
					}
					break
				}
				fmt.Fprintf(c, "{\"ok\":true,\"op\":\"attach\"}\n")
				_, _ = c.Write([]byte("screen bytes\r\n"))
				conns <- c
			}(conn)
		}
	}()

	probe := func(tag string) (net.Conn, chan string) {
		conn, err := net.DialTimeout("unix", sock, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"proto": 1, "op": "attach", "short": "x", "cols": 80, "rows": 24})
		_, _ = conn.Write(append(req, '\n'))
		out := make(chan string, 1)
		go func() {
			acc := make([]byte, 0, 4096)
			b := make([]byte, 4096)
			for {
				_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
				n, err := conn.Read(b)
				acc = append(acc, b[:n]...)
				if err != nil {
					out <- fmt.Sprintf("%s: closed after %d bytes, err=%v, tail=%q", tag, len(acc), err, tailOf(acc, 80))
					return
				}
			}
		}()
		return conn, out
	}

	a, outA := probe("A")
	defer a.Close()
	time.Sleep(500 * time.Millisecond)
	b, _ := probe("B")
	defer b.Close()

	select {
	case msg := <-outA:
		t.Logf("CONTROL FIRST ATTACHER %s", msg)
	case <-time.After(5 * time.Second):
		t.Errorf("CONTROL FIRST ATTACHER A: still open — the probe cannot detect an eviction, so its live result is worthless")
	}
}
