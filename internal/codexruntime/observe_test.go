package codexruntime

import (
	"encoding/json"
	"testing"
)

func notify(t *testing.T, method string, params any) nativeMessage {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return nativeMessage{Method: method, Params: raw}
}

func TestObserverKeepsTheThreadsContextAndTheAccountsLimits(t *testing.T) {
	o := &observer{}
	if ctx, limits := o.usage(); ctx != nil || limits != nil {
		t.Fatal("nothing observed must read as nothing, not zero")
	}
	o.handle(notify(t, "thread/tokenUsage/updated", map[string]any{
		"threadId": nativeThread, "turnId": "t1",
		"tokenUsage": map[string]any{"last": map[string]int{"totalTokens": 51200}, "total": map[string]int{"totalTokens": 90000}, "modelContextWindow": 256000},
	}))
	o.handle(notify(t, "account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{
		"primary":   map[string]any{"usedPercent": 13, "windowDurationMins": 300, "resetsAt": 1_800_000_000},
		"secondary": map[string]any{"usedPercent": 51, "windowDurationMins": 10080},
	}}))
	// A sparse update keeps what it does not mention.
	o.handle(notify(t, "account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{
		"primary": map[string]any{"usedPercent": 20, "windowDurationMins": 300},
	}}))
	ctx, limits := o.usage()
	if ctx == nil || ctx.Tokens != 51200 || ctx.Window != 256000 || ctx.ObservedAt.IsZero() {
		t.Fatalf("context %+v", ctx)
	}
	if limits == nil || len(limits.Windows) != 2 || limits.Windows[0].UsedPercent != 20 || limits.Windows[1].UsedPercent != 51 || limits.Windows[1].Minutes != 10080 {
		t.Fatalf("limits %+v", limits)
	}
	// A usage update without a known window measures nothing.
	o.handle(notify(t, "thread/tokenUsage/updated", map[string]any{"threadId": nativeThread, "turnId": "t2", "tokenUsage": map[string]any{"last": map[string]int{"totalTokens": 7}, "total": map[string]int{"totalTokens": 7}}}))
	if ctx, _ := o.usage(); ctx.Tokens != 51200 {
		t.Fatalf("a reading without a window replaced a real one: %+v", ctx)
	}
}
