package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type liveMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error,omitempty"`
}
type liveControl struct {
	ctx      context.Context
	conn     *websocket.Conn
	sequence int
	pending  []liveMessage
	request  func(liveMessage) any
	owned    map[string]bool
	active   map[string]string
}

func liveTarget(t *testing.T, inference bool) (string, string) {
	t.Helper()
	socket := os.Getenv("FLEETDECK_LIVE_CODEX_SOCKET")
	cwd := os.Getenv("FLEETDECK_LIVE_CODEX_CWD")
	if socket == "" || cwd == "" {
		t.Skip("explicit disposable control socket and cwd required")
	}
	if inference && os.Getenv("FLEETDECK_LIVE_CODEX_INFERENCE") != "1" {
		t.Skip("explicit live inference opt-in required")
	}
	home := os.Getenv("FLEETDECK_LIVE_CODEX_HOME")
	if !filepath.IsAbs(socket) || !filepath.IsAbs(cwd) || !filepath.IsAbs(home) {
		t.Fatal("live target paths must be absolute")
	}
	// The cwd marker alone cannot stop a socket from addressing the user's
	// real daemon: the socket must live inside a marked disposable CODEX_HOME.
	userHome, _ := os.UserHomeDir()
	for _, real := range []string{os.Getenv("CODEX_HOME"), filepath.Join(userHome, ".codex")} {
		if real != "" && filepath.Clean(real) == filepath.Clean(home) {
			t.Fatal("live probe refuses the user's real CODEX_HOME")
		}
	}
	if rel, err := filepath.Rel(home, socket); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		t.Fatal("live control socket is outside the disposable CODEX_HOME")
	}
	for _, dir := range []string{cwd, home} {
		marker, err := os.ReadFile(filepath.Join(dir, ".fleetdeck-probe"))
		if err != nil || string(marker) != "disposable Fleetdeck integration probe\n" {
			t.Fatal("not a disposable probe directory")
		}
	}
	return socket, cwd
}
func openLiveControl(ctx context.Context, t *testing.T, socket string) *liveControl {
	t.Helper()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	conn, _, err := websocket.Dial(ctx, "ws://localhost/", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		transport.CloseIdleConnections()
		t.Fatal(err)
	}
	conn.SetReadLimit(4 << 20)
	t.Cleanup(func() { _ = conn.CloseNow(); transport.CloseIdleConnections() })
	c := &liveControl{ctx: ctx, conn: conn, owned: map[string]bool{}, active: map[string]string{}}
	c.call(t, "initialize", map[string]any{"clientInfo": map[string]string{"name": "fleetdeck_probe", "version": "0.0.0"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil)
	c.write(t, map[string]any{"method": "initialized"})
	return c
}
func (c *liveControl) write(t *testing.T, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.conn.Write(c.ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}
func (c *liveControl) read(t *testing.T) liveMessage {
	t.Helper()
	_, data, err := c.conn.Read(c.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m liveMessage
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	c.recordActivity(m)
	return m
}
func (c *liveControl) handleRequest(t *testing.T, m liveMessage) bool {
	t.Helper()
	if m.Method == "" || len(m.ID) == 0 {
		return false
	}
	if !c.canResolve(m) {
		t.Fatalf("server request %s is not scoped to an owned probe thread", m.Method)
	}
	if c.request == nil {
		t.Fatalf("unsupported probe request %s", m.Method)
	}
	c.write(t, map[string]any{"id": m.ID, "result": c.request(m)})
	return true
}
func (c *liveControl) call(t *testing.T, method string, params any, result any) {
	t.Helper()
	c.sequence++
	expected := fmt.Sprint(c.sequence)
	c.write(t, map[string]any{"id": c.sequence, "method": method, "params": params})
	for {
		m := c.read(t)
		if c.handleRequest(t, m) {
			continue
		}
		if string(m.ID) == expected {
			if m.Error != nil {
				t.Fatalf("RPC %s failed with code %d", method, m.Error.Code)
			}
			if result != nil {
				if err := json.Unmarshal(m.Result, result); err != nil {
					t.Fatal(err)
				}
			}
			return
		}
		if len(c.pending) >= 256 {
			// Streaming deltas can outpace a polling loop; only control events must survive.
			if m.Method == "item/commandExecution/outputDelta" {
				continue
			}
			t.Fatal("probe event queue exceeded limit")
		}
		c.pending = append(c.pending, m)
	}
}
func (c *liveControl) until(t *testing.T, match func(liveMessage) bool) {
	t.Helper()
	for {
		var m liveMessage
		if len(c.pending) > 0 {
			m = c.pending[0]
			c.pending = c.pending[1:]
		} else {
			m = c.read(t)
		}
		if c.handleRequest(t, m) {
			continue
		}
		if match(m) {
			return
		}
	}
}

type liveThread struct {
	ID        string `json:"id"`
	CWD       string `json:"cwd"`
	SessionID string `json:"sessionId"`
	Status    struct {
		Type string `json:"type"`
	} `json:"status"`
}
type liveThreadResult struct {
	Thread liveThread `json:"thread"`
	Model  string     `json:"model"`
}

func (c *liveControl) start(t *testing.T, cwd, name string) liveThreadResult {
	t.Helper()
	var result liveThreadResult
	c.call(t, "thread/start", map[string]any{"cwd": cwd, "sandbox": "read-only", "approvalPolicy": "on-request", "developerInstructions": "Disposable Fleetdeck probe. Do not read files. Only use tools explicitly requested by the test prompt.", "serviceName": "fleetdeck_probe"}, &result)
	if result.Thread.ID == "" || !sameCWD(result.Thread.CWD, cwd) {
		t.Fatal("thread registration did not match the disposable target")
	}
	c.owned[result.Thread.ID] = true
	c.call(t, "thread/name/set", map[string]string{"threadId": result.Thread.ID, "name": name}, nil)
	t.Logf("disposable probe thread: %s", result.Thread.ID)
	return result
}
func waitTurn(c *liveControl, t *testing.T, thread, turn, status string) {
	t.Helper()
	c.until(t, func(m liveMessage) bool {
		if m.Method != "turn/completed" {
			return false
		}
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"turn"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			t.Fatal(err)
		}
		if p.ThreadID != thread || p.Turn.ID != turn {
			return false
		}
		if p.Turn.Status != status {
			t.Fatalf("turn completed as %s, want %s", p.Turn.Status, status)
		}
		return true
	})
}
func TestControlLiveRegistersDistinctThreads(t *testing.T) {
	socket, cwd := liveTarget(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := openLiveControl(ctx, t, socket)
	first := c.start(t, cwd, "Fleetdeck registration probe A")
	second := c.start(t, cwd, "Fleetdeck registration probe B")
	if first.Thread.ID == second.Thread.ID {
		t.Fatal("two launches share their native thread ID")
	}
	for _, id := range []string{first.Thread.ID, second.Thread.ID} {
		var result liveThreadResult
		c.call(t, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &result)
		if result.Thread.ID != id || result.Thread.Status.Type != "idle" {
			t.Fatal("new thread is not independently readable and idle")
		}
	}
}
func TestControlLiveUserInput(t *testing.T) {
	socket, cwd := liveTarget(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	c := openLiveControl(ctx, t, socket)
	thread := c.start(t, cwd, "Fleetdeck user input probe")
	inFlight := false
	defer func() {
		if inFlight {
			c.cleanupOwnTurn(t, socket, thread.Thread.ID)
		}
	}()
	answered := false
	c.request = func(m liveMessage) any {
		if m.Method != "item/tool/requestUserInput" {
			t.Fatalf("unexpected request %s", m.Method)
		}
		var p struct {
			ThreadID  string `json:"threadId"`
			Questions []struct {
				ID      string `json:"id"`
				Options []struct {
					Label string `json:"label"`
				} `json:"options"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			t.Fatal(err)
		}
		if p.ThreadID != thread.Thread.ID || len(p.Questions) != 1 {
			t.Fatal("unexpected user input target")
		}
		q := p.Questions[0]
		found := false
		for _, option := range q.Options {
			if option.Label == "A" {
				found = true
			}
		}
		if !found {
			t.Fatal("probe did not offer the expected harmless answer")
		}
		answered = true
		return map[string]any{"answers": map[string]any{q.ID: map[string]any{"answers": []string{"A"}}}}
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	inFlight = true
	c.call(t, "turn/start", map[string]any{"threadId": thread.Thread.ID, "effort": "low", "collaborationMode": map[string]any{"mode": "plan", "settings": map[string]any{"model": thread.Model, "reasoning_effort": "low"}}, "input": []map[string]string{{"type": "text", "text": "Call request_user_input once: question id probe_marker, header Marker, question Choose a test marker, options labeled exactly A and B, with descriptions First test marker and Second test marker. After the answer, reply USER_INPUT_DONE. Do not call other tools or read files."}}}, &result)
	c.active[thread.Thread.ID] = result.Turn.ID
	waitTurn(c, t, thread.Thread.ID, result.Turn.ID, "completed")
	inFlight = false
	if !answered {
		t.Fatal("turn finished without the structured user-input exchange")
	}
	var read liveThreadResult
	c.call(t, "thread/read", map[string]any{"threadId": thread.Thread.ID, "includeTurns": false}, &read)
	if read.Thread.Status.Type != "idle" {
		t.Fatal("answered thread did not return to idle")
	}
}

func (c *liveControl) canResolve(m liveMessage) bool {
	var identity struct {
		ThreadID string `json:"threadId"`
	}
	return len(m.ID) > 0 && json.Unmarshal(m.Params, &identity) == nil && c.owned[identity.ThreadID]
}
func (c *liveControl) recordActivity(m liveMessage) {
	if m.Method != "turn/started" && m.Method != "turn/completed" {
		return
	}
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(m.Params, &p) != nil || !c.owned[p.ThreadID] || p.Turn.ID == "" {
		return
	}
	if m.Method == "turn/started" {
		c.active[p.ThreadID] = p.Turn.ID
	} else if c.active[p.ThreadID] == p.Turn.ID {
		delete(c.active, p.ThreadID)
	}
}
func runningProbeTurn(response map[string]json.RawMessage) (string, error) {
	data, ok := response["data"]
	if !ok || string(data) == "null" {
		return "", fmt.Errorf("turn listing omitted its data")
	}
	var turns []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &turns); err != nil {
		return "", err
	}
	active := ""
	for _, turn := range turns {
		switch turn.Status {
		case "inProgress":
			if turn.ID == "" || active != "" {
				return "", fmt.Errorf("ambiguous active probe turn")
			}
			active = turn.ID
		case "completed", "interrupted", "failed":
		default:
			return "", fmt.Errorf("unknown probe turn lifecycle")
		}
	}
	if cursor := response["nextCursor"]; len(cursor) > 0 && string(cursor) != "null" && string(cursor) != `""` {
		return "", fmt.Errorf("probe turn listing unexpectedly needs pagination")
	}
	return active, nil
}
func TestControlRequestsCannotResolveForeignThreads(t *testing.T) {
	c := &liveControl{owned: map[string]bool{"own": true}, active: map[string]string{}}
	for _, id := range []json.RawMessage{json.RawMessage(`0`), json.RawMessage(`"request"`)} {
		if !c.canResolve(liveMessage{ID: id, Params: json.RawMessage(`{"threadId":"own"}`)}) {
			t.Fatal("own request rejected")
		}
		if c.canResolve(liveMessage{ID: id, Params: json.RawMessage(`{"threadId":"foreign"}`)}) {
			t.Fatal("foreign request accepted")
		}
	}
	c.recordActivity(liveMessage{Method: "turn/started", Params: json.RawMessage(`{"threadId":"own","turn":{"id":"before-ack"}}`)})
	c.recordActivity(liveMessage{Method: "turn/started", Params: json.RawMessage(`{"threadId":"foreign","turn":{"id":"other"}}`)})
	if c.active["own"] != "before-ack" || len(c.active) != 1 {
		t.Fatalf("active turns: %+v", c.active)
	}
}
func TestControlRecoversTurnIDWithoutStartAck(t *testing.T) {
	id, err := runningProbeTurn(map[string]json.RawMessage{"data": json.RawMessage(`[{"id":"native-turn","status":"inProgress"}]`), "nextCursor": json.RawMessage(`null`)})
	if err != nil || id != "native-turn" {
		t.Fatalf("recovery=%q err=%v", id, err)
	}
	if _, err = runningProbeTurn(map[string]json.RawMessage{}); err == nil {
		t.Fatal("missing listing treated as no active work")
	}
}
