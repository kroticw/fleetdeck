package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestControlUsesUnixWebSocketAndFiltersCWD(t *testing.T) {
	dir, err := os.MkdirTemp("", "fd-probe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, acceptErr := websocket.Accept(w, r, nil)
		if acceptErr != nil {
			t.Error(acceptErr)
			return
		}
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		read := func() map[string]json.RawMessage {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				t.Error(readErr)
				return nil
			}
			var msg map[string]json.RawMessage
			if decodeErr := json.Unmarshal(data, &msg); decodeErr != nil {
				t.Error(decodeErr)
			}
			return msg
		}
		msg := read()
		if string(msg["method"]) != `"initialize"` {
			t.Errorf("first request: %s", msg["method"])
		}
		if writeErr := conn.Write(ctx, websocket.MessageText, []byte(`{"id":1,"result":{"userAgent":"probe"}}`)); writeErr != nil {
			t.Error(writeErr)
			return
		}
		msg = read()
		if string(msg["method"]) != `"initialized"` {
			t.Errorf("notification: %s", msg["method"])
		}
		msg = read()
		var params struct {
			CWD            []string `json:"cwd"`
			UseStateDBOnly bool     `json:"useStateDbOnly"`
		}
		if decodeErr := json.Unmarshal(msg["params"], &params); decodeErr != nil {
			t.Error(decodeErr)
		}
		if len(params.CWD) != 1 || params.CWD[0] != "/test/probe" || !params.UseStateDBOnly {
			t.Errorf("list params: %+v", params)
		}
		if writeErr := conn.Write(ctx, websocket.MessageText, []byte(`{"id":2,"result":{"data":[{"id":"own-thread","cwd":"/test/probe","status":{"type":"idle"}},{"id":"foreign-thread","cwd":"/other","preview":"PRIVATE"}],"nextCursor":null}}`)); writeErr != nil {
			t.Error(writeErr)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := inspectControl(ctx, socket, "/test/probe")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 1 || got.Threads[0].ID != "own-thread" || got.Threads[0].Status != "idle" {
		t.Fatalf("report: %+v", got)
	}
}

func TestControlUnavailableDoesNotStartDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := inspectControl(ctx, filepath.Join(t.TempDir(), "missing.sock"), "/test/probe")
	if err == nil {
		t.Fatal("missing socket reported available")
	}
}

func TestControlCWDHandlesSymlinkSpelling(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	alias := filepath.Join(dir, "alias")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	aliases := cwdSpellings(alias)
	if len(aliases) != 2 || aliases[0] != alias || aliases[1] != canonical {
		t.Fatalf("aliases: %v", aliases)
	}
	if !sameCWD(target, alias) {
		t.Fatal("same directory rejected")
	}
	if sameCWD(target, dir) {
		t.Fatal("parent directory accepted")
	}
}
