//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestASurfaceCallCarriesItsPromiseBindingAndArgument(t *testing.T) {
	call, err := parseSurfaceCall(`{"id":7,"name":"fleetdeckOpen","args":{"kind":"card","path":"a.md"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if call.ID != 7 || call.Name != "fleetdeckOpen" || string(call.Args) != `{"kind":"card","path":"a.md"}` {
		t.Fatalf("call = %+v", call)
	}
}

func TestAMessageThatIsNotACallIsRefused(t *testing.T) {
	for _, message := range []string{`not json`, `{"name":"fleetdeckOpen"}`, `{"id":3}`, `{"id":-1,"name":"x"}`} {
		if _, err := parseSurfaceCall(message); !errors.Is(err, errNotACall) {
			t.Fatalf("%s: err = %v, want errNotACall", message, err)
		}
	}
}

func TestTheReplySettlesThePromiseWithTheAnswerOrTheError(t *testing.T) {
	if got := replyScript(4, map[string]string{"ok": "yes"}, nil); got != `window.fleetdeckHost&&window.fleetdeckHost._reply(4,true,{"ok":"yes"})` {
		t.Fatalf("answer = %s", got)
	}
	if got := replyScript(5, nil, errors.New(`no "such" binding`)); got != `window.fleetdeckHost&&window.fleetdeckHost._reply(5,false,"no \"such\" binding")` {
		t.Fatalf("error = %s", got)
	}
}

func TestASurfaceCallIsAnsweredForTheSurfaceThatMadeIt(t *testing.T) {
	b := newBridge()
	b.handle("fleetdeckOpen", func(surface string, args json.RawMessage) (any, error) {
		return surface + " " + string(args), nil
	})
	got := answerSurfaceCall(b, "sessions", `{"id":2,"name":"fleetdeckOpen","args":"x"}`)
	if got != `window.fleetdeckHost&&window.fleetdeckHost._reply(2,true,"sessions \"x\"")` {
		t.Fatalf("reply = %s", got)
	}
	if got := answerSurfaceCall(b, "sessions", `{"id":3,"name":"fleetdeckTeleport","args":null}`); got != `window.fleetdeckHost&&window.fleetdeckHost._reply(3,false,"unknown binding: fleetdeckTeleport")` {
		t.Fatalf("unknown = %s", got)
	}
	if got := answerSurfaceCall(b, "sessions", `hello`); got != "" {
		t.Fatalf("not a call = %q, want nothing", got)
	}
}

func TestAMessageForThePageIsHandedToItsHost(t *testing.T) {
	got := receiveScript(map[string]any{"type": "folded", "folded": true})
	want := `window.fleetdeckHost&&window.fleetdeckHost.receive({"folded":true,"type":"folded"})`
	if got != want {
		t.Fatalf("script = %s", got)
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(got[len("window.fleetdeckHost&&window.fleetdeckHost.receive("):len(got)-1]), &back); err != nil {
		t.Fatalf("the message is not JSON: %v", err)
	}
}
