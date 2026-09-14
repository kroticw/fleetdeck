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

func TestBindingNamesAreSortedSoTheInjectedScriptIsStable(t *testing.T) {
	b := newBridge()
	for _, n := range []string{"fleetdeckReload", "fleetdeckOpen", "fleetdeckLayout"} {
		b.handle(n, func(string, json.RawMessage) (any, error) { return nil, nil })
	}
	if got := b.names(); !reflect.DeepEqual(got, []string{"fleetdeckLayout", "fleetdeckOpen", "fleetdeckReload"}) {
		t.Fatalf("names = %v", got)
	}
}
