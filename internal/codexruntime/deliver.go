package codexruntime

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sync"
	"time"

	"github.com/kroticw/fleetdeck/internal/executor"
)

const (
	maxSendText = 64 << 10
	// shortcut: the journal keeps 1024 operations; an ID pruned after that
	// many newer sends is treated as new. Add tombstones if callers ever
	// retry IDs that old.
	maxOperations = 1024
	// An unresolved operation this old may be dropped to make room, so
	// unknowns never block every later send.
	staleOperation = time.Hour
	// shortcut: recovery scans the newest pages only; a lost ack found
	// deeper than this stays unknown. Widen if long sessions need it.
	recoveryPages = 5
)

var operationID = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

var errAlreadyJournaled = errors.New("operation already journaled")

// runtimeState serializes changes to the saved record between the service
// loop and concurrent IPC handlers. A change is kept only once saved.
type runtimeState struct {
	mu     sync.Mutex
	owner  *Owner
	record Record
}

func (s *runtimeState) snapshot() Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.record
	r.Operations = maps.Clone(r.Operations)
	return r
}

func (s *runtimeState) update(change func(*Record) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.record
	next.Operations = maps.Clone(s.record.Operations)
	if err := change(&next); err != nil {
		return err
	}
	if err := s.owner.Save(next); err != nil {
		return err
	}
	s.record = next
	return nil
}

// deliver sends text as a new turn exactly once per operation ID. The
// operation is journaled before the write; a lost reply is unknown and is
// resolved only by finding the client message ID in the thread's history,
// never by sending again.
func deliver(ctx context.Context, native *nativeControl, state *runtimeState, op, text string) (executor.Receipt, error) {
	if !operationID.MatchString(op) || text == "" || len(text) > maxSendText {
		return executor.Receipt{}, ErrInvalidFrame
	}
	record := state.snapshot()
	if prev, ok := record.Operations[op]; ok {
		if prev.Phase == string(executor.Accepted) || prev.Phase == string(executor.Failed) {
			return receiptOf(op, prev), nil
		}
		turn, found := findDelivered(ctx, native, record.ThreadID, op)
		if !found {
			return executor.Receipt{OperationID: op, State: executor.DeliveryUnknown}, nil
		}
		return settle(state, op, executor.Accepted, turn)
	}
	var raced *Operation
	err := state.update(func(r *Record) error {
		if r.Operations == nil {
			r.Operations = map[string]Operation{}
		}
		// A concurrent send of the same ID journaled first and owns it.
		if prev, ok := r.Operations[op]; ok {
			raced = &prev
			return errAlreadyJournaled
		}
		if !prune(r.Operations, time.Now()) {
			return executor.ErrConflict
		}
		r.Operations[op] = Operation{Kind: "send", Phase: string(executor.Queued), MessageID: op, Receipt: executor.Queued, UpdatedAt: time.Now().UTC()}
		return nil
	})
	if raced != nil {
		return receiptOf(op, *raced), nil
	}
	if err != nil {
		return executor.Receipt{}, err
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{"threadId": record.ThreadID, "clientUserMessageId": op, "input": []map[string]string{{"type": "text", "text": text}}}
	err = native.call(ctx, "turn/start", params, &started)
	var rejected *nativeRPCError
	switch {
	case err == nil:
		return settle(state, op, executor.Accepted, started.Turn.ID)
	case errors.As(err, &rejected), errors.Is(err, executor.ErrUnavailable):
		// Rejected by Codex, or never written: safe to send under a new ID.
		return settle(state, op, executor.Failed, "")
	default:
		return settle(state, op, executor.DeliveryUnknown, "")
	}
}

func settle(state *runtimeState, op string, phase executor.DeliveryState, turn string) (executor.Receipt, error) {
	var settled Operation
	err := state.update(func(r *Record) error {
		settled = r.Operations[op]
		// A reconcile may already have proven acceptance; never undo it.
		if settled.Phase == string(executor.Accepted) {
			return nil
		}
		settled.Phase, settled.Receipt, settled.UpdatedAt = string(phase), phase, time.Now().UTC()
		if turn != "" {
			settled.TurnID = turn
		}
		r.Operations[op] = settled
		return nil
	})
	if err != nil {
		// The phase reached Codex but not the journal: the caller cannot rely
		// on it and must not resend under a new ID.
		return executor.Receipt{OperationID: op, State: executor.DeliveryUnknown}, fmt.Errorf("%w: %w", executor.ErrDeliveryUnknown, err)
	}
	return receiptOf(op, settled), nil
}

func receiptOf(op string, o Operation) executor.Receipt {
	r := executor.Receipt{OperationID: op, State: executor.DeliveryState(o.Phase)}
	if r.State == executor.Accepted {
		r.NativeMessageID = o.MessageID
	}
	return r
}

// prune drops the oldest settled operations above the limit. Unresolved
// ones are kept while recent: they still need reconciliation.
func prune(ops map[string]Operation, now time.Time) bool {
	for len(ops) >= maxOperations {
		oldest := ""
		for id, o := range ops {
			settled := o.Phase == string(executor.Accepted) || o.Phase == string(executor.Failed)
			if !settled && now.Sub(o.UpdatedAt) < staleOperation {
				continue
			}
			if oldest == "" || o.UpdatedAt.Before(ops[oldest].UpdatedAt) {
				oldest = id
			}
		}
		if oldest == "" {
			return false
		}
		delete(ops, oldest)
	}
	return true
}

// findDelivered looks for the user message Codex stores under the client
// message ID. Any read failure is no evidence either way.
func findDelivered(ctx context.Context, native *nativeControl, thread, op string) (string, bool) {
	cursor := ""
	for range recoveryPages {
		params := map[string]any{"threadId": thread, "itemsView": "full", "limit": 20}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data []struct {
				ID    string `json:"id"`
				Items []struct {
					ID       string `json:"id"`
					ClientID string `json:"clientId"`
					Type     string `json:"type"`
				} `json:"items"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if native.call(ctx, "thread/turns/list", params, &page) != nil {
			return "", false
		}
		for _, turn := range page.Data {
			for _, item := range turn.Items {
				if item.Type == "userMessage" && (item.ClientID == op || item.ID == op) {
					return turn.ID, true
				}
			}
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			return "", false
		}
		cursor = *page.NextCursor
	}
	return "", false
}
