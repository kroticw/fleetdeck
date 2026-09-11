package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kroticw/fleetdeck/internal/config"
)

// The operator's rule, 2026-09-11: the panel lives exactly as long as the
// window that started it -- quit the app and the panel goes, open it and a
// panel starts from that app's own bundle. The panel keeps the rule itself:
// started with --owner-pid, it goes when that process does, however it went.

func TestOwnerGoneFiresWhenTheOwnerExitsAndNotBefore(t *testing.T) {
	owner := exec.Command("sleep", "60")
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Process.Kill(); _ = owner.Wait() })

	gone := ownerGone(owner.Process.Pid)
	// Control: while the owner lives, nothing fires.
	select {
	case <-gone:
		t.Fatal("ownerGone fired while its owner was alive")
	case <-time.After(500 * time.Millisecond):
	}

	_ = owner.Process.Kill()
	select {
	case <-gone:
	case <-time.After(2 * time.Second):
		t.Fatal("ownerGone did not fire within 2s of its owner being killed")
	}
}

func TestOwnerGoneOfAProcessAlreadyGoneFiresAtOnce(t *testing.T) {
	owner := exec.Command("true")
	if err := owner.Run(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ownerGone(owner.Process.Pid):
	case <-time.After(time.Second):
		t.Fatal("ownerGone of a finished process did not fire")
	}
}

// A panel told its owner is a process that is not its parent does not start:
// the window starts the panel itself, directly, and anything in between -- a
// shell wrapping the start for its environment, say -- would leave the panel
// watching the wrong process and outliving the window. Said, not guessed.
//
// run is called in this process, so a run that did not refuse would start a
// whole panel here. It would do so on a home, a board and a daemon socket of
// its own, never the machine's: a broken check -- a mutation run makes exactly
// that -- must not reach the operator's ~/.claude, board or fleet daemon (the
// daemon is found by uid, not by HOME; only -stand-socket keeps a panel off
// it).
func TestAPanelWhoseOwnerIsNotItsParentRefusesToStart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	noDaemon := filepath.Join(dir, "no-daemon.sock")
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.Default()
	cfg.ServerPort = freePort(t)
	cfg.UsageEnabled = false
	cfg.BoardPath = filepath.Join(dir, "board")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	notParent := os.Getppid() + 1_000_000
	err := run(cfgPath, noDaemon, notParent)
	if err == nil || !strings.Contains(err.Error(), "--owner-pid "+strconv.Itoa(notParent)) || !strings.Contains(err.Error(), "parent") {
		t.Fatalf("run with a foreign owner: %v; want a refusal naming the owner and the parent", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// --- the real panel binary, with a shell standing in for the window ---------

type panelRig struct {
	t    *testing.T
	bin  string
	cfg  string
	home string
	port int
	// noDaemon is the -stand-socket the rig's panels get: a path nothing
	// listens on, so they never find the machine's fleet daemon.
	noDaemon string
}

func newPanelRig(t *testing.T) *panelRig {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the panel binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fleetdeck")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ServerPort = freePort(t)
	cfg.UsageEnabled = false
	cfg.BoardPath = filepath.Join(dir, "board")
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	return &panelRig{t: t, bin: bin, cfg: cfgPath, home: home, port: cfg.ServerPort, noDaemon: filepath.Join(dir, "no-daemon.sock")}
}

// window starts a shell that starts the panel as its own direct child with
// --owner-pid of the shell -- the shape the window gives it -- and prints the
// panel's PID.
//
// The shell stays until it is signalled, like a window, or until this test
// process is gone: a test binary killed by its -timeout runs no Cleanup, and
// a shell left behind would keep its panel alive -- by the very rule under
// test.
func (r *panelRig) window() (shell *exec.Cmd, panelPID int) {
	r.t.Helper()
	script := fmt.Sprintf(`%q --config %q --stand-socket %q --owner-pid $$ >/dev/null 2>&1 & echo $!; while kill -0 %d 2>/dev/null; do sleep 0.2; done`, r.bin, r.cfg, r.noDaemon, os.Getpid())
	shell = exec.Command("sh", "-c", script)
	shell.Env = append(os.Environ(), "HOME="+r.home)
	out, err := shell.StdoutPipe()
	if err != nil {
		r.t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = shell.Process.Kill(); _ = shell.Wait() })
	var line [32]byte
	n, _ := out.Read(line[:])
	pid, err := strconv.Atoi(strings.TrimSpace(string(line[:n])))
	if err != nil {
		r.t.Fatalf("no panel pid from the stand-in window: %q", line[:n])
	}
	r.t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if !r.waitAnswer(true, 10*time.Second) {
		r.t.Fatal("the panel never answered")
	}
	return shell, pid
}

func (r *panelRig) answers() bool {
	c := &http.Client{Timeout: 300 * time.Millisecond}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/snapshot", r.port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func (r *panelRig) waitAnswer(want bool, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if r.answers() == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// waitGone waits for pid to be gone; the panel's parent (the shell) is gone
// too, so nothing reaps it but launchd, which does at once.
func waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// panelGrace is how long a panel whose window has gone has to be gone itself:
// its own graceful shutdown bound and a second.
const panelGrace = shutdownTimeout + time.Second

func TestAPanelGoesWhenItsWindowQuits(t *testing.T) {
	r := newPanelRig(t)
	win, panel := r.window()

	// The panel says whose it is: a window finding it answering tells a
	// window's panel from one started in a terminal by this.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/snapshot", r.port))
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Build struct {
			Owner int `json:"owner"`
		} `json:"build"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&snap)
	_ = resp.Body.Close()
	if snap.Build.Owner != win.Process.Pid {
		t.Fatalf("the panel reports owner %d, want its window %d", snap.Build.Owner, win.Process.Pid)
	}

	// Control: a window that stays is a panel that stays.
	time.Sleep(3 * time.Second)
	if !alive(panel) || !r.answers() {
		t.Fatal("the panel went while its window was still there")
	}

	_ = win.Process.Signal(syscall.SIGTERM)
	if !waitGone(panel, panelGrace) || r.answers() {
		t.Fatalf("the panel (pid %d) outlived its window by more than %s", panel, panelGrace)
	}
}

func TestAPanelGoesWhenItsWindowIsKilledAndTheNextWindowStartsOneFreshPanel(t *testing.T) {
	r := newPanelRig(t)
	win, first := r.window()

	_ = win.Process.Signal(syscall.SIGKILL)
	if !waitGone(first, panelGrace) {
		t.Fatalf("the panel (pid %d) outlived a window killed with SIGKILL", first)
	}

	_, second := r.window()
	if second == first || !alive(second) {
		t.Fatalf("the next window's panel is pid %d, want a fresh one (the first was %d)", second, first)
	}
	if alive(first) {
		t.Fatal("two panels: the first one is still there")
	}
}
