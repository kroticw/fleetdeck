package codexruntime

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/kroticw/fleetdeck/internal/executor"
)

// History is one page of a thread's conversation, oldest first; Next pages
// further back.
type History struct {
	Entries []executor.Entry `json:"entries"`
	Next    string           `json:"next,omitempty"`
}

type historyTurn struct {
	ID     string            `json:"id"`
	Status string            `json:"status"`
	Items  []json.RawMessage `json:"items"`
}

func listTurns(ctx context.Context, native *nativeControl, thread, cursor string, limit int) ([]historyTurn, string, error) {
	params := map[string]any{"threadId": thread, "itemsView": "full", "sortDirection": "desc", "limit": max(1, min(limit, 50))}
	if cursor != "" {
		params["cursor"] = cursor
	}
	var page struct {
		Data       []historyTurn `json:"data"`
		NextCursor *string       `json:"nextCursor"`
	}
	if err := native.call(ctx, "thread/turns/list", params, &page); err != nil {
		return nil, "", err
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	return page.Data, next, nil
}

type historyItem struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Text    string `json:"text"`
	Phase   string `json:"phase"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// entry keeps only conversation text: tool calls, reasoning and outputs are
// not history entries.
func entry(raw json.RawMessage) (executor.Entry, bool) {
	var item historyItem
	if json.Unmarshal(raw, &item) != nil {
		return executor.Entry{}, false
	}
	switch item.Type {
	case "userMessage":
		var parts []string
		for _, c := range item.Content {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			}
		}
		return executor.Entry{ID: item.ID, Role: "user", Text: strings.Join(parts, "\n")}, true
	case "agentMessage":
		return executor.Entry{ID: item.ID, Role: "assistant", Text: item.Text}, true
	}
	return executor.Entry{}, false
}

func readHistory(ctx context.Context, native *nativeControl, thread, cursor string, limit int) (History, error) {
	turns, next, err := listTurns(ctx, native, thread, cursor, limit)
	if err != nil {
		return History{}, err
	}
	h := History{Next: next}
	for _, turn := range slices.Backward(turns) {
		for _, raw := range turn.Items {
			if e, ok := entry(raw); ok {
				h.Entries = append(h.Entries, e)
			}
		}
	}
	return h, nil
}

// lastWords is the final answer of the newest finished turn. A finished turn
// without one is NoFinalAnswer; failing to read is an error, not silence.
func lastWords(ctx context.Context, native *nativeControl, thread string) (executor.Said, error) {
	turns, _, err := listTurns(ctx, native, thread, "", 10)
	if err != nil {
		return executor.Said{}, err
	}
	for _, turn := range turns {
		if turn.Status == "inProgress" {
			continue
		}
		words := ""
		for _, raw := range turn.Items {
			var item historyItem
			// Commentary is narration; an unset phase is a legacy answer.
			if json.Unmarshal(raw, &item) == nil && item.Type == "agentMessage" && item.Phase != "commentary" && item.Phase != "partial_answer" {
				words = item.Text
			}
		}
		return executor.Said{Words: words, NoFinalAnswer: words == ""}, nil
	}
	return executor.Said{NoFinalAnswer: true}, nil
}
