//go:build darwin

package main

import "encoding/json"

// natives is what carries the controller's effects out: AppKit and the web
// views. glasswindow.go is the real one; effects_test.go has a recording one.
// Every method runs on the main thread.
type natives interface {
	createSurface(kind, url string, glass glassMode)
	destroySurfaces()
	send(surface string, msg map[string]any)
	focus(surface string)
	navigateBoard(url string)
	openExternal(url string)
	reloadSurface(surface string)
	showWindowPage(page string)
	setAppearance(choice string)
	applyGeometry(g geometry)
	saveWidths(w panelWidths)
	setCapsules(model json.RawMessage)
	setFrameMode(m glassMode)
	reloadBoard()
	setDragBand(height float64)
	// boardInsets tells the board its insets for the frame as it is now:
	// the controller's boardInsetsNow, carried out.
	boardInsets()
}

// runEffects carries effects out in the order the controller gave them.
func runEffects(n natives, effects []effect) {
	for _, e := range effects {
		switch e := e.(type) {
		case createSurfaces:
			for _, kind := range sideSurfaces {
				n.createSurface(kind, e.URL, e.Glass)
			}
		case destroySurfaces:
			n.destroySurfaces()
		case sendTo:
			n.send(e.Surface, e.Message)
		case focusSurface:
			n.focus(e.Surface)
		case navigateBoard:
			n.navigateBoard(e.URL)
		case openExternal:
			n.openExternal(e.URL)
		case reloadSurface:
			n.reloadSurface(e.Surface)
		case showWindowPage:
			n.showWindowPage(e.HTML)
		case setAppearance:
			n.setAppearance(e.Choice)
		case applyGeometry:
			n.applyGeometry(e.G)
		case saveWidths:
			n.saveWidths(e.W)
		case setCapsules:
			n.setCapsules(e.Model)
		case setFrameMode:
			n.setFrameMode(e.Mode)
		case reloadBoard:
			n.reloadBoard()
		case setDragBand:
			n.setDragBand(e.Height)
		case sendBoardInsets:
			n.boardInsets()
		}
	}
}
