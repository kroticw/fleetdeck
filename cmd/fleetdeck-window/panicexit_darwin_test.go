package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// mainQueuePanicEnv makes this test binary the window's main thread in
// miniature instead of running tests: a panic inside a block on the main
// dispatch queue, with webview's Destroy waiting below it.
const mainQueuePanicEnv = "FLEETDECK_WINDOW_TEST_MAIN_QUEUE_PANIC"

// Package initialisation runs on the process's main thread, which a block on
// the main dispatch queue needs; TestMain is never reached.
func init() {
	if os.Getenv(mainQueuePanicEnv) == "" {
		return
	}
	log.SetFlags(0)
	// main.go's order: the web view's Destroy deferred first, the guard after
	// it, so the guard runs first.
	defer testWaitLikeDestroy()
	defer panicExit{logf: log.Printf, exit: os.Exit}.guard()
	testPostPanicBlock()
	testRunMainLoop(10)
	log.Print("the run loop returned without running the block")
	os.Exit(5)
}

// A panic in a block on the main queue unwinds into main's deferred calls. The
// web view's Destroy there waits for a block of its own on the main queue,
// which is still inside the block that panicked, so it waits for ever and the
// panic is never printed: the window hung at 97% CPU with nothing in its log.
// The guard deferred after Destroy says what panicked and ends the process
// before Destroy is reached.
func TestAPanicOnTheMainQueueIsLoggedAndEndsTheWindow(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), mainQueuePanicEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	select {
	case err = <-done:
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("the window's main thread hung after a panic on the main queue instead of exiting; it said:\n%s", out.String())
	}

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("exit = %v, want exit status 2; it said:\n%s", err, out.String())
	}
	said := out.String()
	for _, want := range []string{"panic on the main thread", testPanicValue, "goroutine ", "fleetdeckTestPanicInBlock"} {
		if !strings.Contains(said, want) {
			t.Fatalf("the log does not say %q:\n%s", want, said)
		}
	}
}

// The guard only helps in main's deferred calls after the web view's Destroy:
// deferred before it, it runs after Destroy has already hung.
func TestMainGuardsAgainstPanicsAfterDeferringTheWebViewsDestroy(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	destroy, guard := -1, -1
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil {
			continue
		}
		for i, stmt := range fn.Body.List {
			d, ok := stmt.(*ast.DeferStmt)
			if !ok {
				continue
			}
			switch call := types.ExprString(d.Call.Fun); {
			case call == "w.Destroy":
				destroy = i
			case strings.HasPrefix(call, "panicExit{") && strings.HasSuffix(call, ".guard"):
				guard = i
			}
		}
	}
	switch {
	case destroy < 0:
		t.Fatal("main no longer defers w.Destroy(): this test no longer knows where the guard belongs")
	case guard < 0:
		t.Fatal("main defers no panicExit guard")
	case guard < destroy:
		t.Fatalf("main defers the panic guard (statement %d) before w.Destroy() (statement %d): it would run after Destroy hung", guard, destroy)
	}
}
