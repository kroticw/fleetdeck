package codexruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/coder/websocket"
)

// historyNative serves fixed turn pages (newest first) or fails listing.
func historyNative(t *testing.T, turns []map[string]any, fail bool) *nativeControl {
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
			var reply map[string]any
			if fail {
				reply = map[string]any{"id": m.ID, "error": map[string]any{"code": -32603, "message": "boom"}}
			} else {
				var p struct {
					SortDirection string `json:"sortDirection"`
					ItemsView     string `json:"itemsView"`
				}
				_ = json.Unmarshal(m.Params, &p)
				if p.SortDirection != "desc" || p.ItemsView != "full" {
					t.Errorf("history params %s", m.Params)
				}
				reply = map[string]any{"id": m.ID, "result": map[string]any{"data": turns, "nextCursor": "older"}}
			}
			raw, _ = json.Marshal(reply)
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

func user(text string) map[string]any {
	return map[string]any{"type": "userMessage", "id": "u-" + text, "content": []map[string]string{{"type": "text", "text": text}}}
}

func agent(text, phase string) map[string]any {
	m := map[string]any{"type": "agentMessage", "id": "a-" + text, "text": text}
	if phase != "" {
		m["phase"] = phase
	}
	return m
}

func TestHistoryIsChronologicalAndCarriesItsCursor(t *testing.T) {
	native := historyNative(t, []map[string]any{
		{"id": "t2", "status": "completed", "items": []any{user("second"), agent("done", "final_answer")}},
		{"id": "t1", "status": "completed", "items": []any{user("first"), map[string]any{"type": "reasoning", "id": "r"}, agent("ok", "")}},
	}, false)
	h, err := readHistory(context.Background(), native, nativeThread, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range h.Entries {
		got = append(got, e.Role+":"+e.Text)
	}
	want := []string{"user:first", "assistant:ok", "user:second", "assistant:done"}
	if len(got) != len(want) || h.Next != "older" {
		t.Fatalf("history %v next=%q", got, h.Next)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("history %v", got)
		}
	}
}

func TestLastWordsDistinguishesAnswerNoAnswerAndReadFailure(t *testing.T) {
	ctx := context.Background()
	said, err := lastWords(ctx, historyNative(t, []map[string]any{
		{"id": "t3", "status": "inProgress", "items": []any{user("more")}},
		{"id": "t2", "status": "completed", "items": []any{user("go"), agent("thinking", "commentary"), agent("all done", "final_answer")}},
	}, false), nativeThread)
	if err != nil || said.Words != "all done" || said.NoFinalAnswer {
		t.Fatalf("answer: %+v %v", said, err)
	}
	said, err = lastWords(ctx, historyNative(t, []map[string]any{
		{"id": "t2", "status": "interrupted", "items": []any{user("go"), agent("working on it", "commentary")}},
	}, false), nativeThread)
	if err != nil || !said.NoFinalAnswer || said.Words != "" {
		t.Fatalf("no answer: %+v %v", said, err)
	}
	if _, err := lastWords(ctx, historyNative(t, nil, true), nativeThread); err == nil || errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("read failure must be an error of its own: %v", err)
	}
}
