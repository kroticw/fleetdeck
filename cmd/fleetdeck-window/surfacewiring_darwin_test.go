//go:build darwin

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// The wiring below runs on AppKit, which no test drives; these read it. They
// guard glasswindow.go and surface_darwin.go against sliding back to taking a
// side surface's words by its kind alone.

// parsed is file of this package, parsed.
func parsed(t *testing.T, file string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// funcBody is the body of the function or method called name in f.
func funcBody(t *testing.T, f *ast.File, name string) *ast.BlockStmt {
	t.Helper()
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name && fn.Body != nil {
			return fn.Body
		}
	}
	t.Fatalf("no function %s", name)
	return nil
}

// callsWith is whether body calls fun with arg as its argument number at.
func callsWith(body ast.Node, fun string, at int, arg string) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && types.ExprString(call.Fun) == fun && len(call.Args) > at && types.ExprString(call.Args[at]) == arg {
			found = true
		}
		return !found
	})
	return found
}

// The window hands every word from a side surface to the controller under the
// surface's name, where its generation is checked: surfaceNavigatedFrom,
// pageLoadedFrom and navigateFrom. The kind-only methods take any word as the
// surface's shown now, so glasswindow.go does not call them.
func TestTheWindowHandsSurfaceWordsToTheControllerByName(t *testing.T) {
	file := parsed(t, "glasswindow.go")
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

// A surface's generation goes with it from the controller to its web view's
// name, and a page's call comes back to the surface of that generation only.
func TestTheWindowKeepsASurfacesGenerationFromItsMakingToItsCalls(t *testing.T) {
	window := parsed(t, "glasswindow.go")

	message := funcBody(t, window, "surfaceMessage")
	if !callsWith(message, "surfaceNamed", 1, "surface") {
		t.Error("surfaceMessage does not queue a call through surfaceNamed: a call from a surface taken down would reach the one shown now")
	}
	ast.Inspect(message, func(n ast.Node) bool {
		if index, ok := n.(*ast.IndexExpr); ok && types.ExprString(index.X) == "g.surfaces" {
			t.Errorf("surfaceMessage looks a surface up by kind: %s", types.ExprString(index))
		}
		return true
	})
	if !callsWith(funcBody(t, window, "answerCalls"), "answerSurfaceCall", 1, "surfaceName(kind, s.gen)") {
		t.Error("answerCalls does not answer under the surface's name with its generation: its page's load report would not tell its generation")
	}
	if !callsWith(funcBody(t, window, "createSurface"), "newSurface", 3, "gen") {
		t.Error("createSurface does not make the surface of the generation it is given")
	}

	surface := funcBody(t, parsed(t, "surface_darwin.go"), "newSurface")
	if !callsWith(surface, "C.CString", 0, "surfaceName(kind, gen)") {
		t.Error("newSurface does not name its web view for its kind and generation: every word from it would be dropped as no surface's")
	}
	keeps := false
	ast.Inspect(surface, func(n ast.Node) bool {
		if kv, ok := n.(*ast.KeyValueExpr); ok && types.ExprString(kv.Key) == "gen" && types.ExprString(kv.Value) == "gen" {
			keeps = true
		}
		return true
	})
	if !keeps {
		t.Error("newSurface does not keep its generation on the surface: its calls would find no queue")
	}
}
