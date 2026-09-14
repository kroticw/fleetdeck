//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestTheHostScriptNamesTheSurfaceAndGlassAtVersionOne(t *testing.T) {
	js := hostScript("sessions", glassModeGlass, nil)
	for _, want := range []string{`version:1`, `surface:"sessions"`, `glass:"glass"`, `receive:function`} {
		if !strings.Contains(js, want) {
			t.Fatalf("script lacks %s:\n%s", want, js)
		}
	}
}

func TestTheHostScriptDefinesEveryBindingAsAPromiseOverTheMessageHandler(t *testing.T) {
	js := hostScript("orchestrator", glassModeOpaque, []string{"fleetdeckOpen", "fleetdeckReload"})
	for _, want := range []string{`"fleetdeckOpen"`, `"fleetdeckReload"`, `messageHandlers.fleetdeck.postMessage`, `new Promise`} {
		if !strings.Contains(js, want) {
			t.Fatalf("script lacks %s", want)
		}
	}
}

func TestTheHostScriptLeavesAnExistingHostAlone(t *testing.T) {
	if !strings.HasPrefix(hostScript("board", glassModeGlass, nil), "(function(){if(window.fleetdeckHost)return;") {
		t.Fatal("a second injection must not replace the host a page already wired")
	}
}

func TestTheHostScriptRefusesAnUnknownSurface(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown surface must panic at build time, not load a page that mounts nothing")
		}
	}()
	hostScript("header", glassModeGlass, nil)
}
