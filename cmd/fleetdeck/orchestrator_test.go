package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kroticw/fleetdeck/internal/config"
	"github.com/kroticw/fleetdeck/internal/orchestrator"
)

// claudeThatRecords writes a claude into its own directory that prints what
// `claude --bg` prints and leaves a file behind to say it ran.
func claudeThatRecords(t *testing.T, short string) (bin, ran string) {
	t.Helper()
	dir := t.TempDir()
	bin = filepath.Join(dir, "claude")
	ran = filepath.Join(dir, "ran")
	// Builtins only: a test may leave PATH holding nothing but this claude.
	script := "#!/bin/sh\n: > '" + ran + "'\necho 'backgrounded · " + short + " · orchestrator'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, ran
}

func ran(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// A stand is a panel next to a live fleet. Whatever starts sessions must be
// unable to start one there unless the stand names its own claude — the
// same rule -stand-socket keeps for the daemon, for the same reason: a
// session started by a stand is a real session on the operator's machine.
func TestAStandWithoutItsOwnClaudeStartsNoSession(t *testing.T) {
	onPath, ranOnPath := claudeThatRecords(t, "0a1b2c3d")
	t.Setenv("PATH", filepath.Dir(onPath))
	if start := sessionStarter(runOpts{standSocket: "/tmp/no.sock"}); start != nil {
		_, _ = start(context.Background(), t.TempDir(), "orchestrator")
		t.Error("a stand given no claude of its own can start sessions")
	}
	if ran(ranOnPath) {
		t.Error("a stand ran the claude on PATH")
	}
}

func TestAStandStartsSessionsOnlyWithItsOwnClaude(t *testing.T) {
	onPath, ranOnPath := claudeThatRecords(t, "11111111")
	t.Setenv("PATH", filepath.Dir(onPath))
	standBin, ranStand := claudeThatRecords(t, "22222222")

	start := sessionStarter(runOpts{standSocket: "/tmp/no.sock", standClaude: standBin})
	if start == nil {
		t.Fatal("a stand with its own claude cannot start sessions")
	}
	short, err := start(context.Background(), t.TempDir(), "orchestrator")
	if err != nil {
		t.Fatal(err)
	}
	if short != "22222222" || !ran(ranStand) {
		t.Errorf("started %q with the stand's claude ran=%v, want 22222222", short, ran(ranStand))
	}
	if ran(ranOnPath) {
		t.Error("a stand ran the claude on PATH")
	}
}

// Not a stand: the claude is looked up when a session is started, so a claude
// installed after the panel started is found without a restart.
func TestAPanelStartsSessionsWithTheClaudeItFinds(t *testing.T) {
	// The places outside PATH and HOME hold the real claude on a developer's
	// machine, and a real `claude --bg` is a real session in their fleet.
	saved := claudePlaces
	claudePlaces = nil
	t.Cleanup(func() { claudePlaces = saved })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	start := sessionStarter(runOpts{})
	if start == nil {
		t.Fatal("a panel cannot start sessions")
	}
	if _, err := start(context.Background(), t.TempDir(), "orchestrator"); err == nil || !strings.Contains(err.Error(), "claude was not found") {
		t.Errorf("with no claude anywhere: err = %v", err)
	}
	onPath, ranOnPath := claudeThatRecords(t, "33333333")
	t.Setenv("PATH", filepath.Dir(onPath))
	short, err := start(context.Background(), t.TempDir(), "orchestrator")
	if err != nil || short != "33333333" || !ran(ranOnPath) {
		t.Errorf("after installing claude: %q, %v, ran=%v", short, err, ran(ranOnPath))
	}
}

// fleetDaemon is a daemon on a unix socket that lists existing, plus a started
// session once started names a file that exists, and records every reply.
type fleetDaemon struct {
	mu      sync.Mutex
	replies []string // "<short> <text>"
}

func (fd *fleetDaemon) serve(t *testing.T, existing, startedShort, started string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
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
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}
				var req map[string]any
				if json.Unmarshal([]byte(line), &req) != nil {
					return
				}
				var resp string
				switch req["op"] {
				case "ping":
					resp = `{"ok":true,"op":"ping","version":"test","proto":1}`
				case "list":
					jobs := `{"short":"` + existing + `","state":"working","detail":"refactoring the parser"}`
					if ran(started) {
						jobs += `,{"short":"` + startedShort + `","state":"idle"}`
					}
					resp = `{"ok":true,"op":"list","jobs":[` + jobs + `]}`
				case "reply":
					fd.mu.Lock()
					fd.replies = append(fd.replies, req["short"].(string)+" "+req["text"].(string))
					fd.mu.Unlock()
					resp = `{"ok":true,"op":"reply"}`
				default:
					return
				}
				_, _ = conn.Write([]byte(resp + "\n"))
			}()
		}
	}()
	return sock
}

// The wizard's back end as the panel wires it: the daemon the panel reads, the
// stand's own claude, the panel's configuration. Both paths end with the same
// line in the daemon's replies and the session in orchestrator.session.
func TestAPanelAppointsBothWaysThroughItsDaemonAndConfiguration(t *testing.T) {
	home := isolateHome(t)
	keyDir := filepath.Join(home, ".claude", "daemon")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "control.key"), []byte("key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	board, docs := filepath.Join(root, "board"), filepath.Join(root, "docs")
	for _, dir := range []string{filepath.Join(board, "cards"), docs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.BoardPath, cfg.DocsPaths = board, []string{docs}
	cfgPath := filepath.Join(home, "config.yaml")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	claude, started := claudeThatRecords(t, "0a1b2c3d")
	fd := &fleetDaemon{}
	sock := fd.serve(t, "22222222", "0a1b2c3d", started)

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, runOpts{configPath: cfgPath, standSocket: sock, standClaude: claude, port: port})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("the panel stopped with %v", err)
		}
	}()
	c := panelClient{t: t, base: fmt.Sprintf("http://127.0.0.1:%d", port)}
	until(t, "the panel", func() (bool, string) {
		code, body := c.do(http.MethodGet, "/api/snapshot", "")
		return code == http.StatusOK, body
	})

	want := orchestrator.Message("ru", filepath.Join(docs, "orchestrator.md"))
	for _, tc := range []struct{ body, short string }{
		{`{"session":"22222222","lang":"ru"}`, "22222222"},
		{`{"new":true,"lang":"ru"}`, "0a1b2c3d"},
	} {
		code, body := c.do(http.MethodPost, "/api/orchestrator", tc.body)
		if code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
			t.Fatalf("%s: %d %s", tc.body, code, body)
		}
		fd.mu.Lock()
		last := fd.replies[len(fd.replies)-1]
		fd.mu.Unlock()
		if last != tc.short+" "+want {
			t.Errorf("%s: the daemon got %q, want %q to %s", tc.body, last, want, tc.short)
		}
		pinned, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if pinned.OrchestratorSession != tc.short {
			t.Errorf("%s: orchestrator.session is %q", tc.body, pinned.OrchestratorSession)
		}
	}
	if len(fd.replies) != 2 {
		t.Errorf("the daemon got %d replies, want 2: %q", len(fd.replies), fd.replies)
	}
	if _, err := os.Stat(filepath.Join(docs, "orchestrator.md")); err != nil {
		t.Errorf("no brief in the docs: %v", err)
	}
}

func TestCheckStandClaude(t *testing.T) {
	cases := []struct {
		name                     string
		socketGiven, claudeGiven bool
		claude                   string
		wantErr                  bool
	}{
		{"neither: every real panel", false, false, "", false},
		{"a stand without a claude: starts no session", true, false, "", false},
		{"a stand with its claude", true, true, "/stand/claude", false},
		{"a claude without a stand: would be ignored, so refused", false, true, "/stand/claude", true},
		{"a stand given an empty claude", true, true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkStandClaude(tc.socketGiven, tc.claudeGiven, tc.claude)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, want error: %v", err, tc.wantErr)
			}
		})
	}
}
