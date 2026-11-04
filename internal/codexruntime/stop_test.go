package codexruntime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// workNative emulates one in-progress turn with a background process.
// interrupt ends the turn unless stuck; clean removes the process unless
// sticky.
type workNative struct {
	mu          sync.Mutex
	turnStatus  string
	terminals   int
	stuck       bool
	sticky      bool
	interrupted []string
	cleaned     int
}

func newWorkNative(t *testing.T, w *workNative) *nativeControl {
	t.Helper()
	socket := nativeServer(t, func(ctx context.Context, conn *websocket.Conn) {
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var m nativeMessage
			if json.Unmarshal(raw, &m) != nil {
				return
			}
			w.mu.Lock()
			var result any = map[string]any{}
			switch m.Method {
			case "thread/turns/list":
				result = map[string]any{"data": []map[string]string{{"id": "turn-1", "status": w.turnStatus}}, "nextCursor": nil}
			case "turn/interrupt":
				var p struct {
					TurnID string `json:"turnId"`
				}
				_ = json.Unmarshal(m.Params, &p)
				w.interrupted = append(w.interrupted, p.TurnID)
				if !w.stuck {
					w.turnStatus = "interrupted"
				}
			case "thread/backgroundTerminals/clean":
				w.cleaned++
				if !w.sticky {
					w.terminals = 0
				}
			case "thread/backgroundTerminals/list":
				data := make([]map[string]any, w.terminals)
				for i := range data {
					data[i] = map[string]any{"processId": "p", "itemId": "i", "osPid": nil}
				}
				result = map[string]any{"data": data, "nextCursor": nil}
			}
			w.mu.Unlock()
			// A client that gave up closes first; that ends the fake quietly.
			raw, _ = json.Marshal(map[string]any{"id": m.ID, "result": result})
			if conn.Write(ctx, websocket.MessageText, raw) != nil {
				return
			}
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, err := openNativeControl(ctx, socket, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	c.own(nativeThread)
	return c
}

func TestStopWorkInterruptsTheActiveTurnAndCleansItsProcesses(t *testing.T) {
	w := &workNative{turnStatus: "inProgress", terminals: 1}
	native := newWorkNative(t, w)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := stopWork(ctx, native, nativeThread); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.interrupted) != 1 || w.interrupted[0] != "turn-1" || w.cleaned != 1 || w.terminals != 0 {
		t.Fatalf("stop: %+v", w)
	}
}

func TestStopWorkOnAnIdleThreadStillCleansProcesses(t *testing.T) {
	w := &workNative{turnStatus: "completed", terminals: 2}
	native := newWorkNative(t, w)
	if err := stopWork(context.Background(), native, nativeThread); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.interrupted) != 0 || w.terminals != 0 {
		t.Fatalf("idle stop: %+v", w)
	}
}

func TestStopWorkFailsWhenWorkSurvives(t *testing.T) {
	for name, w := range map[string]*workNative{
		"turn":    {turnStatus: "inProgress", stuck: true},
		"process": {turnStatus: "completed", terminals: 1, sticky: true},
	} {
		native := newWorkNative(t, w)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := stopWork(ctx, native, nativeThread); err == nil {
			t.Fatalf("%s: stop claimed success while work survived", name)
		}
		cancel()
	}
}
