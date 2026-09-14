//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

type fakeNatives struct{ calls []string }

func (f *fakeNatives) createSurface(kind, url string, glass glassMode) {
	f.calls = append(f.calls, "create "+kind+" "+url+" "+string(glass))
}
func (f *fakeNatives) destroySurfaces() { f.calls = append(f.calls, "destroy") }
func (f *fakeNatives) send(surface string, msg map[string]any) {
	f.calls = append(f.calls, "send "+surface+" "+msg["type"].(string))
}
func (f *fakeNatives) focus(surface string)         { f.calls = append(f.calls, "focus "+surface) }
func (f *fakeNatives) navigateBoard(url string)     { f.calls = append(f.calls, "navigate "+url) }
func (f *fakeNatives) openExternal(url string)      { f.calls = append(f.calls, "external "+url) }
func (f *fakeNatives) reloadSurface(surface string) { f.calls = append(f.calls, "reload "+surface) }
func (f *fakeNatives) showWindowPage(page string)   { f.calls = append(f.calls, "page "+page) }
func (f *fakeNatives) setAppearance(choice string)  { f.calls = append(f.calls, "appearance "+choice) }
func (f *fakeNatives) applyGeometry(geometry)       { f.calls = append(f.calls, "geometry") }
func (f *fakeNatives) saveWidths(panelWidths)       { f.calls = append(f.calls, "save") }
func (f *fakeNatives) setCapsules(json.RawMessage)  { f.calls = append(f.calls, "capsules") }
func (f *fakeNatives) setFrameMode(m glassMode)     { f.calls = append(f.calls, "frame "+string(m)) }
func (f *fakeNatives) reloadBoard()                 { f.calls = append(f.calls, "reload board") }
func (f *fakeNatives) setDragBand(h float64) {
	f.calls = append(f.calls, fmt.Sprintf("drag band %v", h))
}

func TestCreatingSurfacesMakesBothColumnsOnTheSameAddress(t *testing.T) {
	f := &fakeNatives{}
	runEffects(f, []effect{createSurfaces{Fleet: "work", URL: "http://127.0.0.1:7777/?fleet=work", Glass: glassModeGlass}})
	want := []string{
		"create orchestrator http://127.0.0.1:7777/?fleet=work glass",
		"create sessions http://127.0.0.1:7777/?fleet=work glass",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestEffectsRunInTheOrderTheControllerGaveThem(t *testing.T) {
	f := &fakeNatives{}
	runEffects(f, []effect{
		destroySurfaces{},
		navigateBoard{URL: "u"},
		openExternal{URL: "https://x"},
		reloadSurface{Surface: "sessions"},
		showWindowPage{HTML: "<h1>x</h1>"},
		setAppearance{Choice: "dark"},
		applyGeometry{},
		saveWidths{},
		setCapsules{},
		setFrameMode{Mode: glassModeOpaque},
		sendTo{Surface: "board", Message: map[string]any{"type": "insets"}},
		focusSurface{Surface: "board"},
		reloadBoard{},
		setDragBand{Height: 40},
	})
	want := []string{
		"destroy", "navigate u", "external https://x", "reload sessions", "page <h1>x</h1>", "appearance dark",
		"geometry", "save", "capsules", "frame opaque", "send board insets", "focus board", "reload board", "drag band 40",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %v", f.calls)
	}
}
