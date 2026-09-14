//go:build darwin

package main

import (
	"testing"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// A person's reload of the board -- Cmd+R, Reload in the menu -- asks for the
// panel's page only where the screen would: a panel answering, and no handover
// still restarting it. Until the PR #166 review it navigated whatever was on
// screen: a window taking over loaded the panel the old window still held, and
// over the keeper's failure page it went to a dead address, whose failure page
// then replaced the keeper's log.

func TestAReloadDuringTheHandoverAsksForNothing(t *testing.T) {
	s, c := newScreen(true)
	// The staged panel answers while the handover is still restarting it.
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	if s.reload() {
		t.Fatal("a reload during the handover asked for the panel's page, which the old window still holds")
	}
	c.t = c.t.Add(navSilentWait * pageLoadTries)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("after a reload during the handover, tick = %v, %q; want nothing until the handover is done", navigate, pageHeading(html))
	}
}

func TestAReloadOverTheKeepersFailurePageAsksForNothing(t *testing.T) {
	s, c := newScreen(false)
	if _, page := s.on(supervisor.Event{State: supervisor.Failed}); page == "" {
		t.Fatal("the keeper's failure is not put up")
	}
	if s.reload() {
		t.Fatal("a reload over the keeper's failure page asked for a panel that does not answer")
	}
	c.t = c.t.Add(navSilentWait * pageLoadTries)
	if navigate, html := s.tick(); navigate || html != "" {
		t.Fatalf("after a reload over the keeper's failure page, tick = %v, %q; want its page left up", navigate, pageHeading(html))
	}
}

func TestAReloadOverTheStartingPageAsksForNothing(t *testing.T) {
	s, _ := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Starting})
	if s.reload() {
		t.Fatal("a reload over the starting page asked for a panel not answering yet")
	}
}

func TestAReloadOfTheShownPanelAsksForItAgainAsAFirstTry(t *testing.T) {
	s, _ := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.pageSays(pagePanel, testURL)
	if !s.reload() || !s.asked || s.tries != 1 {
		t.Fatalf("a reload of the shown panel: asked = %v, tries = %d; want a first try", s.asked, s.tries)
	}
}

// The page saying the panel's page did not load has a button that asks again;
// a reload does the same.
func TestAReloadOverThePageThatDidNotLoadAsksAgain(t *testing.T) {
	s, c := newScreen(false)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	for i := 0; i < pageLoadTries; i++ {
		c.t = c.t.Add(navSilentWait)
		s.tick()
	}
	if s.asked {
		t.Fatal("the page was not given up on")
	}
	if !s.reload() {
		t.Fatal("a reload over the page that did not load does not ask again")
	}
}

func TestAReloadAfterTheHandoverAsksForThePanel(t *testing.T) {
	s, _ := newScreen(true)
	s.on(supervisor.Event{State: supervisor.Answering, Ours: true, PID: 1})
	s.handedOver()
	s.pageSays(pagePanel, testURL)
	if !s.reload() {
		t.Fatal("a reload after the handover does not ask for the panel's page")
	}
}
