//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestABindingIsCalledWithTheSurfaceThatAskedAndItsArguments(t *testing.T) {
	b := newBridge()
	var gotSurface string
	var gotArgs map[string]string
	b.handle("fleetdeckOpen", func(surface string, args json.RawMessage) (any, error) {
		gotSurface = surface
		return "ok", json.Unmarshal(args, &gotArgs)
	})
	answer, err := b.call("sessions", "fleetdeckOpen", json.RawMessage(`{"kind":"card","path":"a.md"}`))
	if err != nil || answer != "ok" {
		t.Fatalf("call = %v, %v", answer, err)
	}
	if gotSurface != "sessions" || !reflect.DeepEqual(gotArgs, map[string]string{"kind": "card", "path": "a.md"}) {
		t.Fatalf("handler saw %q %v", gotSurface, gotArgs)
	}
}

func TestAnUnknownBindingIsRefusedByName(t *testing.T) {
	_, err := newBridge().call("board", "fleetdeckTeleport", nil)
	if !errors.Is(err, errUnknownBinding) {
		t.Fatalf("err = %v, want errUnknownBinding", err)
	}
}

func TestABoardOnlyBindingRefusesASideSurfaceAndIsNotDefinedInIt(t *testing.T) {
	b := newBridge()
	called := 0
	b.handleBoard("fleetdeckLayout", func(string, json.RawMessage) (any, error) {
		called++
		return nil, nil
	})
	b.handle("fleetdeckOpen", func(string, json.RawMessage) (any, error) { return nil, nil })
	for _, surface := range []string{"orchestrator", "sessions"} {
		if _, err := b.call(surface, "fleetdeckLayout", json.RawMessage(`{"version":1,"mode":"panel","fleet":"x"}`)); !errors.Is(err, errBoardOnly) {
			t.Fatalf("the %s surface calling fleetdeckLayout: err = %v, want errBoardOnly", surface, err)
		}
	}
	if _, err := b.call("board", "fleetdeckLayout", nil); err != nil || called != 1 {
		t.Fatalf("the board calling fleetdeckLayout: err = %v, handler called %d times, want once", err, called)
	}
	if got := b.surfaceNames(); !reflect.DeepEqual(got, []string{"fleetdeckOpen"}) {
		t.Fatalf("surface names = %v, want only the bindings a surface may call", got)
	}
}

func TestBindingNamesAreSortedSoTheInjectedScriptIsStable(t *testing.T) {
	b := newBridge()
	for _, n := range []string{"fleetdeckReload", "fleetdeckOpen", "fleetdeckLayout"} {
		b.handle(n, func(string, json.RawMessage) (any, error) { return nil, nil })
	}
	if got := b.names(); !reflect.DeepEqual(got, []string{"fleetdeckLayout", "fleetdeckOpen", "fleetdeckReload"}) {
		t.Fatalf("names = %v", got)
	}
}
