//go:build darwin

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// The window hands every word from a side surface to the controller under the
// surface's name, where its generation is checked: surfaceNavigatedFrom,
// pageLoadedFrom and navigateFrom. The kind-only methods take any word as the
// surface's shown now, so glasswindow.go does not call them. The wiring runs on
// AppKit, which no test drives; this reads it.
func TestTheWindowHandsSurfaceWordsToTheControllerByName(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "glasswindow.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]bool{"g.ctl.surfaceNavigatedFrom": false, "g.ctl.pageLoadedFrom": false, "g.ctl.navigateFrom": false}
	kindOnly := map[string]bool{"g.ctl.surfaceNavigated": true, "g.ctl.pageLoaded": true, "g.ctl.navigate": true}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn := types.ExprString(call.Fun)
		if _, ok := byName[fn]; ok {
			byName[fn] = true
		}
		if kindOnly[fn] {
			t.Errorf("glasswindow.go calls %s: a word from a surface taken down would be taken as the shown one's", fn)
		}
		return true
	})
	for fn, called := range byName {
		if !called {
			t.Errorf("glasswindow.go no longer calls %s", fn)
		}
	}
}
