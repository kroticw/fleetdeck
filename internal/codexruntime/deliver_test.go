package codexruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/executor"
)

const testOperation = "7d2f6c1e-3b4a-4c5d-8e9f-0a1b2c3d4e5f"

// deliverNative answers turn/start with reply (nil drops the connection
// instead, losing the ack) and lists history holding delivered message IDs.
func deliverNative(t *testing.T, reply map[string]any, delivered []string, starts *atomic.Int32) *nativeControl {
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
			switch m.Method {
			case "turn/start":
				starts.Add(1)
				var p struct {
					ClientUserMessageID string `json:"clientUserMessageId"`
					ThreadID            string `json:"threadId"`
				}
				_ = json.Unmarshal(m.Params, &p)
				if p.ClientUserMessageID != testOperation || p.ThreadID != nativeThread {
					t.Errorf("turn/start params %s", m.Params)
				}
				if reply == nil {
					_ = conn.CloseNow()
					return
				}
				answer := map[string]any{"id": m.ID}
				for k, v := range reply {
					answer[k] = v
				}
				nativeWrite(ctx, t, conn, answer)
			case "thread/turns/list":
				items := []map[string]string{}
				for _, id := range delivered {
					// "client:<op>" stores the op only as clientId, beside its own item ID.
					if op, ok := strings.CutPrefix(id, "client:"); ok {
						items = append(items, map[string]string{"id": "item-own", "clientId": op, "type": "userMessage"})
						continue
					}
					items = append(items, map[string]string{"id": id, "type": "userMessage"})
				}
				nativeWrite(ctx, t, conn, map[string]any{"id": m.ID, "result": map[string]any{"data": []map[string]any{{"id": "turn-r", "status": "completed", "items": items}}, "nextCursor": nil}})
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

func deliverState(t *testing.T) *runtimeState {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fleetdeck-deliver-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store, err := OpenStore(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.Acquire(testRuntimeKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	record := runtimeRecord()
	record.ThreadID, record.Lifecycle = nativeThread, "live"
	if err := owner.Save(record); err != nil {
		t.Fatal(err)
	}
	return &runtimeState{owner: owner, record: record}
}

func TestDeliverAcceptsOnTurnStartAndNeverSendsTwice(t *testing.T) {
	var starts atomic.Int32
	native := deliverNative(t, map[string]any{"result": map[string]any{"turn": map[string]string{"id": "turn-9"}}}, nil, &starts)
	state := deliverState(t)
	for range 2 {
		receipt, err := deliver(context.Background(), native, state, testOperation, "do the task")
		if err != nil || receipt.State != executor.Accepted || receipt.OperationID != testOperation {
			t.Fatalf("receipt %+v %v", receipt, err)
		}
	}
	if starts.Load() != 1 {
		t.Fatalf("one operation started %d turns", starts.Load())
	}
	op := state.snapshot().Operations[testOperation]
	if op.Phase != string(executor.Accepted) || op.TurnID != "turn-9" || op.MessageID != testOperation {
		t.Fatalf("journal %+v", op)
	}
}

func TestDeliverLostAckIsUnknownAndRecoveredFromHistory(t *testing.T) {
	var starts atomic.Int32
	state := deliverState(t)
	lost := deliverNative(t, nil, nil, &starts)
	receipt, err := deliver(context.Background(), lost, state, testOperation, "do the task")
	if err != nil || receipt.State != executor.DeliveryUnknown {
		t.Fatalf("lost ack: %+v %v", receipt, err)
	}
	if state.snapshot().Operations[testOperation].Phase != string(executor.DeliveryUnknown) {
		t.Fatal("unknown delivery was not journaled")
	}
	recovered := deliverNative(t, nil, []string{testOperation}, &starts)
	receipt, err = deliver(context.Background(), recovered, state, testOperation, "do the task")
	if err != nil || receipt.State != executor.Accepted {
		t.Fatalf("recovery: %+v %v", receipt, err)
	}
	if starts.Load() != 1 {
		t.Fatal("recovery re-sent the task")
	}
}

func TestDeliverUnknownStaysUnknownWithoutEvidence(t *testing.T) {
	var starts atomic.Int32
	state := deliverState(t)
	if _, err := deliver(context.Background(), deliverNative(t, nil, nil, &starts), state, testOperation, "x"); err != nil {
		t.Fatal(err)
	}
	receipt, err := deliver(context.Background(), deliverNative(t, nil, []string{"other-message"}, &starts), state, testOperation, "x")
	if err != nil || receipt.State != executor.DeliveryUnknown || starts.Load() != 1 {
		t.Fatalf("no evidence: %+v %v starts=%d", receipt, err, starts.Load())
	}
}

func TestDeliverRejectedTurnFails(t *testing.T) {
	var starts atomic.Int32
	native := deliverNative(t, map[string]any{"error": map[string]any{"code": -32600, "message": "busy"}}, nil, &starts)
	receipt, err := deliver(context.Background(), native, deliverState(t), testOperation, "x")
	if err != nil || receipt.State != executor.Failed {
		t.Fatalf("rejected: %+v %v", receipt, err)
	}
}

func TestDeliverValidatesOperationAndText(t *testing.T) {
	var starts atomic.Int32
	native := deliverNative(t, nil, nil, &starts)
	state := deliverState(t)
	for _, c := range []struct{ op, text string }{{"", "x"}, {"bad id with spaces", "x"}, {testOperation, ""}} {
		if _, err := deliver(context.Background(), native, state, c.op, c.text); err == nil {
			t.Fatalf("accepted %q/%q", c.op, c.text)
		}
	}
	if starts.Load() != 0 {
		t.Fatal("invalid delivery reached Codex")
	}
}

func TestDeliverConcurrentSameOperationStartsOneTurn(t *testing.T) {
	var starts atomic.Int32
	native := deliverNative(t, map[string]any{"result": map[string]any{"turn": map[string]string{"id": "turn-9"}}}, nil, &starts)
	state := deliverState(t)
	done := make(chan executor.Receipt, 4)
	for range 4 {
		go func() {
			r, _ := deliver(context.Background(), native, state, testOperation, "x")
			done <- r
		}()
	}
	for range 4 {
		<-done
	}
	if starts.Load() != 1 {
		t.Fatalf("one operation started %d turns", starts.Load())
	}
}

func TestDeliverRecoversByClientIDAndNeverDowngradesAcceptance(t *testing.T) {
	var starts atomic.Int32
	state := deliverState(t)
	if _, err := deliver(context.Background(), deliverNative(t, nil, nil, &starts), state, testOperation, "x"); err != nil {
		t.Fatal(err)
	}
	clientOnly := deliverNative(t, nil, []string{"client:" + testOperation}, &starts)
	receipt, err := deliver(context.Background(), clientOnly, state, testOperation, "x")
	if err != nil || receipt.State != executor.Accepted {
		t.Fatalf("clientId recovery: %+v %v", receipt, err)
	}
	if receipt, _ := settle(state, testOperation, executor.DeliveryUnknown, ""); receipt.State != executor.Accepted {
		t.Fatalf("accepted operation downgraded to %s", receipt.State)
	}
}

func TestUncertainOutcomesKeepTheirMeaningOverIPC(t *testing.T) {
	if code := errorCode(fmt.Errorf("%w: journal", executor.ErrDeliveryUnknown)); code != "delivery_unknown" {
		t.Fatalf("delivery: %s", code)
	}
	if code := errorCode(ErrReplyUnknown); code != "reply_unknown" {
		t.Fatalf("reply: %s", code)
	}
}
