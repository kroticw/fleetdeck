//go:build darwin

package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/supervisor"
)

// testPanelEnv makes TestMain a stand-in panel listening at the address it
// holds, instead of running tests: a real process on a real port, which a real
// Takeover stops, starts and restarts. testPanelOwnerEnv is the test process,
// which the stand-in outlives by no more than a look.
const (
	testPanelEnv      = "FLEETDECK_WINDOW_TEST_PANEL"
	testPanelOwnerEnv = "FLEETDECK_WINDOW_TEST_PANEL_OWNER"
)

// runTestPanel is the stand-in: it answers every request, and reports in its
// snapshot the revision its bundle's build put beside it, as a panel's build
// fingerprint does.
func runTestPanel(addr string) {
	if owner, err := strconv.Atoi(os.Getenv(testPanelOwnerEnv)); err == nil && owner > 0 {
		go func() {
			for syscall.Kill(owner, 0) == nil {
				time.Sleep(100 * time.Millisecond)
			}
			os.Exit(4)
		}()
	}
	exe, _ := os.Executable()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		os.Exit(3)
	}
	_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/snapshot" {
			rev, _ := os.ReadFile(filepath.Join(filepath.Dir(exe), "revision"))
			_, _ = fmt.Fprintf(w, `{"build":{"web":"stand-in","executable":%q,"revision":%q}}`, exe, strings.TrimSpace(string(rev)))
			return
		}
		_, _ = fmt.Fprint(w, "panel")
	}))
}

// recordingRegistry is LaunchServices as a takeover sees it: what it was told,
// in order.
type recordingRegistry struct {
	mu    sync.Mutex
	calls []string
}

func (r *recordingRegistry) Forget(bundle string) error   { r.add("forget " + bundle); return nil }
func (r *recordingRegistry) Register(bundle string) error { r.add("register " + bundle); return nil }

func (r *recordingRegistry) add(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *recordingRegistry) told() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// takeoverStand is an update's new window without its window: an installed
// bundle and a staged one beside it, each with the stand-in as its panel, and
// a real Takeover with a keeper of its own between them. No panel runs before
// it, so there is nothing old to stop.
type takeoverStand struct {
	tk       *supervisor.Takeover
	events   *supervisor.KeeperEvents
	kept     *keeperRun
	registry *recordingRegistry
}

func newTakeoverStand(t *testing.T, revision string) *takeoverStand {
	t.Helper()
	dir := t.TempDir()
	canonical := filepath.Join(dir, "apps", supervisor.BundleName)
	staged := filepath.Join(supervisor.StagingDir(canonical), supervisor.BundleName)
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	for bundle, rev := range map[string]string{canonical: "old", staged: "new"} {
		bin := supervisor.PanelIn(bundle)
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, self, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(bin), "revision"), []byte(rev), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	url := "http://" + addr + "/"

	events := supervisor.NewKeeperEvents()
	keeper := &supervisor.Keeper{
		URL: url,
		Bin: supervisor.PanelIn(staged),
		// A stand-in built with -race would otherwise sleep a second before it
		// exits, as a panel does not.
		Env:          append(os.Environ(), testPanelEnv+"="+addr, testPanelOwnerEnv+"="+strconv.Itoa(os.Getpid()), "GORACE=atexit_sleep_ms=0"),
		LogPath:      filepath.Join(dir, "panel.log"),
		StartTimeout: 10 * time.Second,
		MinUptime:    time.Minute,
		Poll:         100 * time.Millisecond,
		OnEvent:      events.Push,
	}
	kept := &keeperRun{k: keeper}
	registry := &recordingRegistry{}
	tk := &supervisor.Takeover{
		URL:         url,
		Handover:    supervisor.Handover{Path: filepath.Join(supervisor.StagingDir(canonical), "handover")},
		Staged:      staged,
		Canonical:   canonical,
		Revision:    revision,
		Keeper:      keeper,
		StartKeeper: kept.start,
		Registry:    registry,
		Logf:        t.Logf,
	}
	t.Cleanup(func() {
		kept.stop()
		_ = supervisor.StopHolder(context.Background(), url, time.Second)
	})
	return &takeoverStand{tk: tk, events: events, kept: kept, registry: registry}
}

// steps is what the handover file says so far, by step.
func (s *takeoverStand) steps() []string {
	data, _ := os.ReadFile(s.tk.Handover.Path)
	var steps []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if name, _, _ := strings.Cut(line, "\t"); name != "" {
			steps = append(steps, name)
		}
	}
	return steps
}

func (s *takeoverStand) waitStep(t *testing.T, step string, within time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if steps := s.steps(); slices.Contains(steps, step) {
			return steps
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the handover never said %q; it says %v", step, s.steps())
	return nil
}

func waitClosed(t *testing.T, ch <-chan struct{}, within time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(within):
		t.Fatalf("%s did not happen within %s", what, within)
	}
}

// uiOnThisThread is a window made at once, running what it is handed where it
// is handed.
func uiOnThisThread(handedOver, terminate func()) windowUI {
	return windowUI{dispatch: func(f func()) { f() }, handedOver: handedOver, terminate: terminate}
}

// The whole handover, to done, goes by while the window is still being made:
// the gate stays shut throughout. What done means for the window happens once
// it is made, and LaunchServices is told again then, after the window's own
// check-in.
func TestAWindowStartedByAnUpdateTakesThePanelOverBeforeItsWindowIsMade(t *testing.T) {
	s := newTakeoverStand(t, "new")
	gate := &windowGate{}
	exited := make(chan struct{}, 1)
	ended := startHandover(s.events, s.tk, gate, s.kept.stop, func() { exited <- struct{}{} })

	steps := s.waitStep(t, string(supervisor.StepDone), 20*time.Second)
	if want := []string{"alive", "panel", "swapped", "done"}; !slices.Equal(steps, want) {
		t.Fatalf("with the window not made, the handover says %v, want %v", steps, want)
	}
	registered := s.registry.told()
	if len(registered) != 2 {
		t.Fatalf("the takeover told LaunchServices %v, want the staged path forgotten and the installed one registered", registered)
	}

	handed := make(chan struct{})
	gate.open(uiOnThisThread(func() { close(handed) }, func() { t.Error("the window was closed after a handover that is done") }))
	waitClosed(t, handed, 5*time.Second, "the panel's page asked for once the window is made")
	deadline := time.Now().Add(5 * time.Second)
	for len(s.registry.told()) < 4 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got, want := s.registry.told(), append(registered, registered...); !slices.Equal(got, want) {
		t.Fatalf("once the window is made LaunchServices was told %v, want %v", got, want)
	}
	waitClosed(t, ended, 10*time.Second, "the takeover ending")
	select {
	case <-exited:
		t.Fatal("a window whose handover is done quit")
	default:
	}
}

// A takeover that fails while the window is still being made ends the process
// there: no window is shown, and nothing waits for one. The swap was never made,
// so LaunchServices is told nothing.
func TestAWindowWhoseTakeoverFailsBeforeItsWindowIsMadeQuitsWithoutOne(t *testing.T) {
	s := newTakeoverStand(t, "a-build-that-was-not-built")
	gate := &windowGate{}
	exited := make(chan struct{})
	ended := startHandover(s.events, s.tk, gate, s.kept.stop, func() { close(exited) })

	waitClosed(t, exited, 20*time.Second, "the process quitting")
	steps := s.steps()
	if !slices.Contains(steps, string(supervisor.StepFailed)) || slices.Contains(steps, string(supervisor.StepSwapped)) {
		t.Fatalf("the handover says %v, want failed before the swap", steps)
	}
	waitClosed(t, ended, 10*time.Second, "the takeover ending")
	gate.open(uiOnThisThread(
		func() { t.Error("a failed handover asked for the panel's page") },
		func() { t.Error("a process that has quit closed a window") },
	))
	if told := s.registry.told(); len(told) != 0 {
		t.Fatalf("a takeover that never swapped told LaunchServices %v", told)
	}
}

// A takeover that fails once the window is made closes the window, as before.
func TestAWindowWhoseTakeoverFailsAfterItsWindowIsMadeClosesIt(t *testing.T) {
	s := newTakeoverStand(t, "a-build-that-was-not-built")
	gate := &windowGate{}
	terminated := make(chan struct{})
	gate.open(uiOnThisThread(func() { t.Error("a failed handover asked for the panel's page") }, func() { close(terminated) }))
	ended := startHandover(s.events, s.tk, gate, s.kept.stop, func() { t.Error("a window already made quit without closing") })

	waitClosed(t, terminated, 20*time.Second, "the window closing")
	waitClosed(t, ended, 10*time.Second, "the takeover ending")
}

// main itself keeps the order: the takeover is begun before the web view is
// made, and codesign, which ownTeamID runs, is not asked on main's own path.
func TestMainTakesThePanelOverBeforeItMakesTheWindow(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatal("no func main in main.go")
	}
	var lits []*ast.FuncLit
	ast.Inspect(body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			lits = append(lits, lit)
		}
		return true
	})
	inFuncLit := func(p token.Pos) bool {
		return slices.ContainsFunc(lits, func(lit *ast.FuncLit) bool { return p >= lit.Pos() && p < lit.End() })
	}
	var handover, window token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			switch fun.Name {
			case "startHandover":
				if !inFuncLit(call.Pos()) && handover == token.NoPos {
					handover = call.Pos()
				}
			case "ownTeamID", "updateWay", "updateWayOf":
				if !inFuncLit(call.Pos()) {
					t.Errorf("main calls %s on its own path, at %s", fun.Name, fset.Position(call.Pos()))
				}
			}
		case *ast.SelectorExpr:
			if x, ok := fun.X.(*ast.Ident); ok && x.Name == "webview" && fun.Sel.Name == "New" && window == token.NoPos {
				window = call.Pos()
			}
		}
		return true
	})
	if handover == token.NoPos || window == token.NoPos {
		t.Fatalf("main.go: startHandover at %v, webview.New at %v; want both on main's own path", fset.Position(handover), fset.Position(window))
	}
	if handover > window {
		t.Fatalf("main begins the takeover at %s, after it makes the web view at %s", fset.Position(handover), fset.Position(window))
	}
}
