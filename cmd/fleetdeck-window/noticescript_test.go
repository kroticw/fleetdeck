//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestTheBuildMarkFollowsTheBrandIntoTheOrchestratorSurface(t *testing.T) {
	if noticeScriptFor("board") != noticeScript {
		t.Fatal("the board keeps the notice as it is")
	}
	orchestrator := noticeScriptFor("orchestrator")
	for _, want := range []string{".build-rev::before", noticeBindingName, noticeRepaintFunction} {
		if !strings.Contains(orchestrator, want) {
			t.Fatalf("the orchestrator surface's script lacks %s:\n%s", want, orchestrator)
		}
	}
	if strings.Contains(orchestrator, noticeShownBindingName) || strings.Contains(orchestrator, replaceBindingName) {
		t.Fatal("the orchestrator surface paints the mark only; the notice box and its button stay the board's")
	}
	if noticeScriptFor("sessions") != "" {
		t.Fatal("the sessions surface shows no build and needs no script")
	}
}

func TestTheOrchestratorSurfaceIsGivenTheBuildMarkScript(t *testing.T) {
	b := newBridge()
	if got := len(surfaceScripts("orchestrator", "http://127.0.0.1:7777/", glassModeGlass, b)); got != 3 {
		t.Fatalf("orchestrator scripts = %d, want the host, page load and build mark scripts", got)
	}
	if got := len(surfaceScripts("sessions", "http://127.0.0.1:7777/", glassModeGlass, b)); got != 2 {
		t.Fatalf("sessions scripts = %d, want the host and page load scripts", got)
	}
}
