//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// errUnknownBinding is a call to a name the window never registered: a page of
// another build asking for something this window does not have.
var errUnknownBinding = errors.New("unknown binding")

// bridgeHandler answers one binding for the web view that called it. surface is
// "board", "orchestrator" or "sessions"; args is the call's one argument as the
// page sent it.
type bridgeHandler func(surface string, args json.RawMessage) (any, error)

// bridge is the one place a binding is defined. The board's web view reaches it
// through webview_go's Bind, each surface's web view through its own message
// handler; both land here, so a binding is written once for all three.
type bridge struct {
	mu       sync.Mutex
	handlers map[string]bridgeHandler
}

func newBridge() *bridge { return &bridge{handlers: map[string]bridgeHandler{}} }

func (b *bridge) handle(name string, h bridgeHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[name] = h
}

// names is every registered binding, sorted, so the script that defines them in
// a surface is the same text on every build of the same bindings.
func (b *bridge) names() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.handlers))
	for n := range b.handlers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (b *bridge) call(surface, name string, args json.RawMessage) (any, error) {
	b.mu.Lock()
	h, ok := b.handlers[name]
	b.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", errUnknownBinding, name)
	}
	return h(surface, args)
}
