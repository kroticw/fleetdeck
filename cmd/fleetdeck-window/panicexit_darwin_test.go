package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// panicChildEnv makes this test binary a window in miniature instead of running
// tests, panicking where its value says: "main", inside a block on the main
// dispatch queue with webview's Destroy waiting below it; "callqueue", in a
// side surface's call queue answering a call; "twice", on two goroutines at
// once, the record kept slowly.
const panicChildEnv = "FLEETDECK_WINDOW_TEST_PANIC"

// The build a panicking child says it is.
var testPanicBuild = build{Version: "9.9.9-test", Revision: "0123abcd", Modified: true}

// Package initialisation runs on the process's main thread, which a block on
// the main dispatch queue needs; TestMain is never reached.
func init() {
	where := os.Getenv(panicChildEnv)
	if where == "" {
		return
	}
	log.SetFlags(0)
	home, _ := os.UserHomeDir()
	// As main.go sets it, before anything that can panic starts.
	panics = panicExit{
		logf:  log.Printf,
		exit:  os.Exit,
		file:  panicLogPath(home, false),
		build: testPanicBuild,
		exe:   os.Args[0],
		now:   time.Now,
	}
	switch where {
	case "main":
		// main.go's order: the web view's Destroy deferred first, the guard
		// after it, so the guard runs first.
		defer testWaitLikeDestroy()
		defer panics.in("on the main thread").guard()
		testPostPanicBlock()
		testRunMainLoop(10)
		log.Print("the run loop returned without running the block")
	case "callqueue":
		q := newCallQueue(func(string) { panic(testPanicValue) })
		q.push("a call")
		time.Sleep(10 * time.Second)
		log.Print("the call queue did not end the process")
	case "twice":
		// Whichever keeps its record takes its time over it, as a slow disk
		// would: the other must not end the process meanwhile.
		panics.now = func() time.Time {
			time.Sleep(500 * time.Millisecond)
			return time.Now()
		}
		start := make(chan struct{})
		for i := range 2 {
			go func() {
				defer panics.in(fmt.Sprintf("in test goroutine %d", i)).guard()
				<-start
				panic(testPanicValue)
			}()
		}
		close(start)
		time.Sleep(10 * time.Second)
		log.Print("neither panic ended the process")
	}
	os.Exit(5)
}

// runPanickingChild runs the child panicking where says, with home as its HOME,
// and returns what it said and how it exited; a child still running after 20
// seconds has hung.
func runPanickingChild(t *testing.T, where, home string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), panicChildEnv+"="+where, "HOME="+home)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("the window hung after a panic %s instead of exiting; it said:\n%s", where, out.String())
		return "", nil
	}
}

func wantExitStatus2(t *testing.T, err error, said string) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("exit = %v, want exit status 2; it said:\n%s", err, said)
	}
}

func readPanicFile(t *testing.T, home, said string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "Library", "Logs", "fleetdeck-window-panic.log"))
	if err != nil {
		t.Fatalf("no panic file: %v; the child said:\n%s", err, said)
	}
	return string(raw)
}

func wantAll(t *testing.T, what, in string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(in, want) {
			t.Fatalf("%s does not say %q:\n%s", what, want, in)
		}
	}
}

// A panic in a block on the main queue unwinds into main's deferred calls. The
// web view's Destroy there waits for a block of its own on the main queue,
// which is still inside the block that panicked, so it waits for ever and the
// panic is never printed: the window hung at 97% CPU with nothing in its log.
// The guard deferred after Destroy says what panicked and ends the process
// before Destroy is reached.
func TestAPanicOnTheMainQueueIsLoggedAndEndsTheWindow(t *testing.T) {
	said, err := runPanickingChild(t, "main", t.TempDir())
	wantExitStatus2(t, err, said)
	wantAll(t, "the log", said, "panic on the main thread", testPanicValue, "goroutine ", "fleetdeckTestPanicInBlock")
}

// An app opened from the Dock writes its stderr to /dev/null, so the log alone
// keeps nothing of a panic there: the panic goes to a file under the home's
// Library/Logs, with when, which build, which binary, where, what, and every
// goroutine's stack.
func TestAPanicOnTheMainQueueIsKeptInTheWindowsPanicFile(t *testing.T) {
	home := t.TempDir()
	said, err := runPanickingChild(t, "main", home)
	wantExitStatus2(t, err, said)
	kept := readPanicFile(t, home, said)
	if !regexp.MustCompile(`(?m)^=== \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`).MatchString(kept) {
		t.Fatalf("the record does not start with when it happened:\n%s", kept)
	}
	wantAll(t, "the panic file", kept,
		"version: 9.9.9-test",
		"revision: 0123abcd (modified)",
		"binary: "+os.Args[0],
		"where: on the main thread",
		"panic: "+testPanicValue,
		"goroutine ",
		"fleetdeckTestPanicInBlock",
	)
}

// A panic on a goroutine of the window's own ends the process at once, as it
// always did, but with the same record: from the Dock nothing else of it is
// kept. The call queue here is the side surfaces' own, as glasswindow.go makes
// it.
func TestAPanicOnAWindowGoroutineIsKeptAndEndsTheWindow(t *testing.T) {
	home := t.TempDir()
	said, err := runPanickingChild(t, "callqueue", home)
	wantExitStatus2(t, err, said)
	wantAll(t, "the log", said, "panic in a side surface's call queue", testPanicValue)
	wantAll(t, "the panic file", readPanicFile(t, home, said), "where: in a side surface's call queue", "panic: "+testPanicValue, "goroutine ")
}

// Two goroutines panicking at once: the one that keeps its record finishes it
// before the process ends, however slowly, and the other says nothing and
// exits nothing — ended with it, not before it.
func TestTwoPanicsAtOnceLeaveOneWholeRecord(t *testing.T) {
	home := t.TempDir()
	said, err := runPanickingChild(t, "twice", home)
	wantExitStatus2(t, err, said)
	kept := readPanicFile(t, home, said)
	if n := strings.Count(kept, "=== "); n != 1 {
		t.Fatalf("the panic file holds %d records, want one whole record:\n%s", n, kept)
	}
	wantAll(t, "the panic file", kept, "where: in test goroutine ", "panic: "+testPanicValue, "goroutine ")
	if !strings.HasSuffix(kept, "\n") {
		t.Fatalf("the record was cut short:\n%s", kept)
	}
	if n := strings.Count(said, "the window exits"); n != 1 {
		t.Fatalf("%d panics were said, want the one kept:\n%s", n, said)
	}
}

// A panic file that cannot be written does not keep the window from saying
// the panic and exiting, and does not panic a second time.
func TestAPanicTheFileCannotKeepIsStillLoggedAndEndsTheWindow(t *testing.T) {
	said, err := runPanickingChild(t, "main", "/dev/null")
	wantExitStatus2(t, err, said)
	wantAll(t, "the log", said, "panic on the main thread", testPanicValue, "could not be kept")
	if strings.Count(said, "panic on the main thread") != 1 {
		t.Fatalf("the panic was said more than once:\n%s", said)
	}
}

// recordAt is a guard for tests of the file: a fixed build, binary and time.
func recordAt(file string) panicExit {
	at := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	return panicExit{
		file:  file,
		build: build{Version: "0.10.2"},
		exe:   "/Applications/fleetdeck.app/Contents/MacOS/fleetdeck-window",
		now:   func() time.Time { return at },
	}.in("on the main thread")
}

func freshRecord(t *testing.T) {
	t.Helper()
	panicRecorded.Store(false)
	t.Cleanup(func() { panicRecorded.Store(false) })
}

// The file is appended to and never grows by more than one record a process:
// a panic while the first is being kept adds nothing.
func TestAProcessKeepsOnePanicRecord(t *testing.T) {
	freshRecord(t)
	file := filepath.Join(t.TempDir(), "Library", "Logs", "fleetdeck-window-panic.log")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("an earlier record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := recordAt(file)
	if err := p.record("the first", []byte("goroutine 1 [running]:\n")); err != nil {
		t.Fatal(err)
	}
	if err := p.record("the second", []byte("goroutine 1 [running]:\n")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	kept := string(raw)
	wantAll(t, "the file", kept,
		"an earlier record\n",
		"=== 2026-09-15T12:00:00Z fleetdeck-window panic\n",
		"version: 0.10.2\n",
		"revision: unknown\n",
		"binary: /Applications/fleetdeck.app/Contents/MacOS/fleetdeck-window\n",
		"where: on the main thread\n",
		"panic: the first\n",
	)
	if strings.Contains(kept, "the second") {
		t.Fatalf("a second record was kept in the same process:\n%s", kept)
	}
}

// A file grown past panicFileLimit is set aside as .1, over the one set aside
// before, and the record starts a new file: the file never grows without end.
func TestAPanicFileOverTheLimitIsSetAsideBeforeTheRecord(t *testing.T) {
	freshRecord(t)
	file := filepath.Join(t.TempDir(), "fleetdeck-window-panic.log")
	if err := os.WriteFile(file+".1", []byte("the oldest records\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := append([]byte("the old records\n"), bytes.Repeat([]byte("x"), panicFileLimit)...)
	if err := os.WriteFile(file, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordAt(file).record("the new one", []byte("goroutine 1 [running]:\n")); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(kept, []byte("the old records")) || !bytes.Contains(kept, []byte("panic: the new one\n")) {
		t.Fatalf("the file after setting the old one aside:\n%.200s", kept)
	}
	aside, err := os.ReadFile(file + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(aside, []byte("the old records\n")) || bytes.Contains(aside, []byte("the oldest records")) {
		t.Fatalf("the file set aside:\n%.200s", aside)
	}
}

// A file of exactly panicFileLimit is not over it: the record is appended.
func TestAPanicFileAtTheLimitIsAppendedTo(t *testing.T) {
	freshRecord(t)
	file := filepath.Join(t.TempDir(), "fleetdeck-window-panic.log")
	old := bytes.Repeat([]byte("x"), panicFileLimit)
	if err := os.WriteFile(file, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := recordAt(file).record("the new one", []byte("goroutine 1 [running]:\n")); err != nil {
		t.Fatal(err)
	}
	kept, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(kept, old) || !bytes.Contains(kept, []byte("panic: the new one\n")) {
		t.Fatalf("a file at the limit was not appended to (%d bytes)", len(kept))
	}
	if _, err := os.Stat(file + ".1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file at the limit was set aside: %v", err)
	}
}

// A goroutine that ends without panicking, by returning or by runtime.Goexit,
// passes its guard: nothing is said and nothing exits.
func TestAGuardLetsAGoroutineEndWithoutAPanic(t *testing.T) {
	exited := make(chan int, 2)
	p := panicExit{
		logf: func(format string, args ...any) { t.Errorf("the guard said: "+format, args...) },
		exit: func(code int) { exited <- code },
	}
	for _, end := range []func(){func() {}, runtime.Goexit} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer p.in("in a test goroutine").guard()
			end()
		}()
		<-done
	}
	select {
	case code := <-exited:
		t.Fatalf("the guard exited with %d on a goroutine that did not panic", code)
	default:
	}
}

// A dev app keeps its panics apart from the installed app's, as it keeps its
// logs.
func TestADevAppKeepsItsPanicsInAFileOfItsOwn(t *testing.T) {
	if got := panicLogPath("/Users/o", false); got != "/Users/o/Library/Logs/fleetdeck-window-panic.log" {
		t.Fatalf("installed: %s", got)
	}
	if got := panicLogPath("/Users/o", true); got != "/Users/o/Library/Logs/fleetdeck-dev-window-panic.log" {
		t.Fatalf("dev: %s", got)
	}
}

func isGuard(call ast.Expr) bool {
	s := types.ExprString(call)
	return strings.HasPrefix(s, "panics.in(") && strings.HasSuffix(s, ".guard")
}

func parseWindowFile(t *testing.T, name string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// The main thread's guard is main's last deferred call, so it runs first: before
// the web view's Destroy, which would hang, and before anything else deferred
// after Destroy that a panic could reach.
func TestMainsLastDeferredCallIsThePanicGuard(t *testing.T) {
	destroy, guard, last := -1, -1, -1
	for _, decl := range parseWindowFile(t, "main.go").Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "main" || fn.Recv != nil {
			continue
		}
		for i, stmt := range fn.Body.List {
			d, ok := stmt.(*ast.DeferStmt)
			if !ok {
				continue
			}
			last = i
			switch {
			case types.ExprString(d.Call.Fun) == "w.Destroy":
				destroy = i
			case isGuard(d.Call.Fun):
				guard = i
			}
		}
	}
	switch {
	case destroy < 0:
		t.Fatal("main no longer defers w.Destroy(): this test no longer knows where the guard belongs")
	case guard < 0:
		t.Fatal("main defers no panic guard")
	case guard != last:
		t.Fatalf("main's panic guard is statement %d, and its last deferred call is statement %d: a call deferred after the guard runs before it", guard, last)
	}
}

// Every goroutine the window's own code starts begins with its guard, so a panic
// on any of them is kept before the process ends. Every file of the package is
// read, not a list: a goroutine added anywhere is held to it.
func TestEveryWindowGoroutineStartsWithAPanicGuard(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		ast.Inspect(parseWindowFile(t, name), func(n ast.Node) bool {
			g, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			found++
			lit, ok := g.Call.Fun.(*ast.FuncLit)
			if !ok || len(lit.Body.List) == 0 {
				t.Errorf("%s: `go %s` does not start a function literal that begins with its guard", name, types.ExprString(g.Call.Fun))
				return true
			}
			if d, ok := lit.Body.List[0].(*ast.DeferStmt); !ok || !isGuard(d.Call.Fun) {
				t.Errorf("%s: a goroutine does not begin with `defer panics.in(...).guard()`", name)
			}
			return true
		})
	}
	if found == 0 {
		t.Error("the package starts no goroutine: this test no longer knows what it guards")
	}
}
