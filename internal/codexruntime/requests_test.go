package codexruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/kroticw/fleetdeck/internal/executor"
)

// requestNative sends the given server requests for the owned thread and
// forwards every client answer to answers.
func requestNative(t *testing.T, requests []map[string]any, answers chan<- nativeMessage) *nativeControl {
	t.Helper()
	// Requests for a thread not yet owned are dropped by design, so they are
	// sent only once the client owns it.
	owned := make(chan struct{})
	socket := nativeServer(t, func(ctx context.Context, conn *websocket.Conn) {
		select {
		case <-owned:
		case <-ctx.Done():
			return
		}
		for _, r := range requests {
			nativeWrite(ctx, t, conn, r)
		}
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var m nativeMessage
			if json.Unmarshal(raw, &m) == nil {
				answers <- m
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
	close(owned)
	return c
}

func awaitPending(t *testing.T, c *nativeControl, n int) []PendingRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := pendingRequests(c); len(got) == n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d pending requests, have %v", n, pendingRequests(c))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPendingApprovalIsExposedSafelyAndAnsweredOnce(t *testing.T) {
	answers := make(chan nativeMessage, 4)
	c := requestNative(t, []map[string]any{{
		"id": 0, "method": "item/commandExecution/requestApproval",
		"params": map[string]any{"threadId": nativeThread, "turnId": "turn-1", "itemId": "item-1", "approvalId": "ap-1", "command": "ls -la", "cwd": "/repo", "reason": "list files", "startedAtMs": 1},
	}}, answers)
	pending := awaitPending(t, c, 1)
	r := pending[0]
	if r.Kind != "command" || r.Command != "ls -la" || r.CWD != "/repo" || r.Reason != "list files" || r.TurnID != "turn-1" || r.ItemID != "item-1" || r.Token == "" {
		t.Fatalf("request view: %+v", r)
	}
	ctx := context.Background()
	// A session-wide grant is never accepted from the panel.
	if err := respondRequest(ctx, c, Answer{Token: r.Token, Decision: "acceptForSession"}); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("session grant: %v", err)
	}
	if err := respondRequest(ctx, c, Answer{Token: r.Token, Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-answers:
		if string(m.ID) != "0" || string(m.Result) != `{"decision":"accept"}` {
			t.Fatalf("native answer: id=%s result=%s", m.ID, m.Result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("answer never reached Codex")
	}
	if err := respondRequest(ctx, c, Answer{Token: r.Token, Decision: "decline"}); !errors.Is(err, executor.ErrConflict) {
		t.Fatalf("second answer to a resolved request: %v", err)
	}
}

func TestUserInputAnswersOnlyItsOwnQuestions(t *testing.T) {
	answers := make(chan nativeMessage, 4)
	c := requestNative(t, []map[string]any{{
		"id": "q-7", "method": "item/tool/requestUserInput",
		"params": map[string]any{"threadId": nativeThread, "turnId": "turn-2", "itemId": "item-2", "isBlocking": true, "questions": []map[string]any{
			{"id": "color", "header": "Color", "question": "Which color?", "options": []map[string]string{{"label": "red", "description": "warm"}}},
		}},
	}}, answers)
	r := awaitPending(t, c, 1)[0]
	if r.Kind != "userInput" || len(r.Questions) != 1 || r.Questions[0].ID != "color" || r.Questions[0].Options[0] != "red" {
		t.Fatalf("question view: %+v", r)
	}
	ctx := context.Background()
	if err := respondRequest(ctx, c, Answer{Token: r.Token, Answers: map[string][]string{"size": {"big"}}}); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("answer to an unasked question: %v", err)
	}
	if err := respondRequest(ctx, c, Answer{Token: r.Token, Decision: "accept"}); !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("approval decision for a question: %v", err)
	}
	if err := respondRequest(ctx, c, Answer{Token: r.Token, Answers: map[string][]string{"color": {"red"}}}); err != nil {
		t.Fatal(err)
	}
	m := <-answers
	if string(m.ID) != `"q-7"` || string(m.Result) != `{"answers":{"color":{"answers":["red"]}}}` {
		t.Fatalf("native answer: id=%s result=%s", m.ID, m.Result)
	}
}

func TestUnknownRequestKindsWaitForAHumanButCannotBeAnswered(t *testing.T) {
	c := requestNative(t, []map[string]any{{
		"id": 3, "method": "item/permissions/requestApproval",
		"params": map[string]any{"threadId": nativeThread, "turnId": "turn-3", "itemId": "item-3"},
	}}, make(chan nativeMessage, 1))
	r := awaitPending(t, c, 1)[0]
	if r.Kind != "other" || r.Method != "item/permissions/requestApproval" {
		t.Fatalf("other view: %+v", r)
	}
	if err := respondRequest(context.Background(), c, Answer{Token: r.Token, Decision: "accept"}); !errors.Is(err, executor.ErrUnsupported) {
		t.Fatalf("answer to an unsupported request: %v", err)
	}
}
