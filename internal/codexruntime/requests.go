package codexruntime

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/kroticw/fleetdeck/internal/executor"
)

// The request types are the executor contract's: a panel shows and answers
// them the same way whichever executor asks.
type (
	PendingRequest = executor.PendingRequest
	Question       = executor.Question
	Answer         = executor.Answer
)

var requestKinds = map[string]string{
	"item/commandExecution/requestApproval": "command",
	"item/fileChange/requestApproval":       "fileChange",
	"item/tool/requestUserInput":            "userInput",
}

// Session-wide and policy-amending grants are deliberately absent.
var approvalDecisions = []string{"accept", "decline", "cancel"}

func pendingRequests(native *nativeControl) []PendingRequest {
	messages := native.pending()
	out := make([]PendingRequest, 0, len(messages))
	for _, m := range messages {
		out = append(out, requestView(m))
	}
	return out
}

func requestView(m nativeMessage) PendingRequest {
	var p struct {
		TurnID     string `json:"turnId"`
		ItemID     string `json:"itemId"`
		ApprovalID string `json:"approvalId"`
		Command    string `json:"command"`
		CWD        string `json:"cwd"`
		Reason     string `json:"reason"`
		GrantRoot  string `json:"grantRoot"`
		Questions  []struct {
			Question
			Options []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	kind := requestKinds[m.Method]
	if kind == "" || json.Unmarshal(m.Params, &p) != nil {
		kind = "other"
	}
	r := PendingRequest{Token: m.Token, Kind: kind, Method: m.Method, TurnID: p.TurnID, ItemID: p.ItemID, ApprovalID: p.ApprovalID, Command: p.Command, CWD: p.CWD, Reason: p.Reason, GrantRoot: p.GrantRoot}
	for _, q := range p.Questions {
		view := q.Question
		view.Options = nil
		for _, o := range q.Options {
			view.Options = append(view.Options, o.Label)
		}
		r.Questions = append(r.Questions, view)
	}
	return r
}

// respondRequest validates an answer against the request it names and writes
// it once. A stale or already answered token is a conflict.
func respondRequest(ctx context.Context, native *nativeControl, a Answer) error {
	var request *PendingRequest
	for _, r := range pendingRequests(native) {
		if r.Token == a.Token {
			request = &r
			break
		}
	}
	if request == nil {
		return executor.ErrConflict
	}
	var result any
	switch request.Kind {
	case "command", "fileChange":
		if !slices.Contains(approvalDecisions, a.Decision) || a.Answers != nil {
			return ErrInvalidFrame
		}
		result = map[string]string{"decision": a.Decision}
	case "userInput":
		if a.Decision != "" || len(a.Answers) == 0 {
			return ErrInvalidFrame
		}
		answers := map[string]map[string][]string{}
		for id, values := range a.Answers {
			if !slices.ContainsFunc(request.Questions, func(q Question) bool { return q.ID == id }) {
				return ErrInvalidFrame
			}
			answers[id] = map[string][]string{"answers": values}
		}
		result = map[string]any{"answers": answers}
	default:
		return executor.ErrUnsupported
	}
	return native.respond(ctx, a.Token, result)
}
