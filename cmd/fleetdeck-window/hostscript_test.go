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

// A stand's frame with the new card form open is taken without a press: the
// host object says so on a stand, and never in a person's window.
func TestTheHostScriptAsksForTheNewCardFormOnlyOnAStandThatOpensIt(t *testing.T) {
	defer func(stand bool, open string) { hostOnStand, hostStandOpen = stand, open }(hostOnStand, hostStandOpen)
	for _, c := range []struct {
		stand bool
		open  string
		want  bool
	}{
		{stand: true, open: "newcard", want: true},
		{stand: true, open: "", want: false},
		{stand: false, open: "newcard", want: false},
	} {
		hostOnStand, hostStandOpen = c.stand, c.open
		got := strings.Contains(hostScript("board", glassModeGlass, nil), `host.standOpen="newcard";`)
		if got != c.want {
			t.Errorf("stand %v, open %q: the script asks for the form: %v, want %v", c.stand, c.open, got, c.want)
		}
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
