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

// errBoardOnly is a side surface calling a binding only the board's page may:
// what the board reports of itself.
var errBoardOnly = errors.New("binding is the board's only")

// bridgeHandler answers one binding for the web view that called it. surface is
// "board", or a side surface's name with its generation, "sessions@3"
// (surfacename.go); args is the call's one argument as the page sent it.
type bridgeHandler func(surface string, args json.RawMessage) (any, error)

// bridge is the one place a binding is defined. The board's web view reaches it
// through webview_go's Bind, each surface's web view through its own message
// handler; both land here, so a binding is written once for all three.
type bridge struct {
	mu       sync.Mutex
	handlers map[string]bridgeHandler
	// boardOnly names the bindings registered with handleBoard.
	boardOnly map[string]bool
}

func newBridge() *bridge {
	return &bridge{handlers: map[string]bridgeHandler{}, boardOnly: map[string]bool{}}
}

func (b *bridge) handle(name string, h bridgeHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[name] = h
	delete(b.boardOnly, name)
}

// handleBoard registers a binding only the board's page may call: what the
// board reports of itself -- its layout, its capsules, its theme. A side
// surface calling it is refused, and its host script does not define it
// (surfaceNames): a surface's page reporting a layout would frame the window
// on that surface's word.
func (b *bridge) handleBoard(name string, h bridgeHandler) {
	b.handle(name, func(surface string, args json.RawMessage) (any, error) {
		if surface != "board" {
			return nil, fmt.Errorf("%w: %s, called from the %s surface", errBoardOnly, name, surface)
		}
		return h(surface, args)
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	b.boardOnly[name] = true
}

// surfaceNames is every binding a side surface may call, sorted: all but the
// board's own.
func (b *bridge) surfaceNames() []string {
	all := b.names()
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(all))
	for _, n := range all {
		if !b.boardOnly[n] {
			out = append(out, n)
		}
	}
	return out
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
