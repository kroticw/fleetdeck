package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A stand-in panel: this test binary, run again with an environment variable
// that makes TestMain listen on a port instead of running tests. Real
// process, real port, real signals -- the parts of starting and stopping a
// panel that a fake could not show.
const helperEnv = "FLEETDECK_SUPERVISOR_HELPER"

// ownerEnv carries the PID of the test process that started a stand-in. The
// stand-ins are started as panels are, in a session of their own (Setsid), so
// nothing that ends a test binary reaches them -- which is exactly what kept
// them running after a test binary was killed by its -timeout, when no
// t.Cleanup runs: 19 of them were found on the operator's machine, left by
// mutation runs, an hour old. A stand-in whose test process is gone now exits
// by itself, as a real panel does when its window is gone.
const ownerEnv = "FLEETDECK_SUPERVISOR_HELPER_OWNER"

// helperEnvFor is the environment a stand-in of kind is started with.
func helperEnvFor(kind, addr string) []string {
	return append(os.Environ(), helperEnv+"="+kind+"@"+addr, ownerEnv+"="+strconv.Itoa(os.Getpid()))
}

// watchOwner ends the stand-in once the test process that started it is gone.
func watchOwner() {
	owner, err := strconv.Atoi(os.Getenv(ownerEnv))
	if err != nil || owner <= 0 {
		return
	}
	go func() {
		for {
			if syscall.Kill(owner, 0) != nil {
				os.Exit(4)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// reportOwnerEnv is the window PID a stand-in reports as its owner in its
// snapshot, as a panel started by a window does.
const reportOwnerEnv = "FLEETDECK_SUPERVISOR_HELPER_REPORT_OWNER"

// termNotice is what the stand-in prints to its log when SIGTERM reaches it.
const termNotice = "helper: SIGTERM, leaving"

// crashNotice is the last thing the "crash" stand-in says before it dies.
const crashNotice = "helper: config is broken, giving up"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		runHelper(mode)
		return
	}
	os.Exit(m.Run())
}

func runHelper(mode string) {
	watchOwner()
	kind, addr, _ := strings.Cut(mode, "@")
	if kind == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		go func() {
			<-term
			fmt.Println(termNotice)
			os.Exit(0)
		}()
	}
	switch kind {
	case "crash":
		// A panel that dies before it ever listens: a broken config, a bad build.
		fmt.Fprintln(os.Stderr, crashNotice)
		os.Exit(3)
	case "silent":
		// A panel that runs but never answers where it is looked for -- a port
		// in its configuration other than the one the window asks.
		select {}
	}
	exe, _ := os.Executable()
	fmt.Printf("helper ppid: %d\n", os.Getppid())
	fmt.Println("helper exe: " + exe)
	fmt.Println("helper stdout: listening on " + addr)
	fmt.Fprintln(os.Stderr, "helper stderr: listening on "+addr)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		os.Exit(3)
	}
	_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/snapshot" {
			// What a real panel's snapshot carries, as far as telling a panel
			// from any other program goes -- whose window it says it is, and
			// its revision, which a built stand-in finds in a file its build
			// put beside it.
			owner, _ := strconv.Atoi(os.Getenv(reportOwnerEnv))
			rev := ""
			if b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "revision")); err == nil {
				rev = strings.TrimSpace(string(b))
			}
			fmt.Fprintf(w, `{"build":{"web":"stand-in","executable":%q,"owner":%d,"revision":%q}}`, exe, owner, rev)
			return
		}
		fmt.Fprint(w, "panel")
	}))
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func startHelper(t *testing.T, kind string) (*Panel, string) {
	t.Helper()
	return startHelperLogging(t, kind, filepath.Join(t.TempDir(), "panel.log"))
}

func startHelperLogging(t *testing.T, kind, logPath string) (*Panel, string) {
	t.Helper()
	addr := freeAddr(t)
	p, err := StartPanel(os.Args[0], nil, helperEnvFor(kind, addr), logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(p.PID, syscall.SIGKILL) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := WaitAnswer(ctx, "http://"+addr+"/"); err != nil {
		t.Fatalf("the started panel never answered: %v", err)
	}
	return p, addr
}

// The log is where anyone looks when a panel did not come up: both of its
// streams go there, and a restart adds to it rather than wiping what the
// previous panel said before it died. (That a started panel answers at all
// is checked by every test here, in startHelper.)
func TestAStartedPanelWritesBothStreamsToItsLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "panel.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const earlier = "an earlier panel's last words\n"
	if err := os.WriteFile(logPath, []byte(earlier), 0o644); err != nil {
		t.Fatal(err)
	}
	p, addr := startHelperLogging(t, "listen", logPath)
	if err := p.Stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, want := range []string{earlier, "helper stdout: listening on " + addr, "helper stderr: listening on " + addr} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q; it holds:\n%s", want, log)
		}
	}
}

// Signals meant for the window are not the panel's: the panel goes when its
// window goes, by its own graceful shutdown (cmd/fleetdeck, owner.go), not by
// a Ctrl+C delivered to the window's whole process group. A process started
// with Setsid leads a new session and, with it, a new process group -- and a
// process group is what such signals are delivered to. The group is what is
// checked:
// syscall has Getpgid on every platform this runs on, Getsid only on darwin
// (the first version of this test used Getsid and did not compile on the
// Linux CI leg).
func TestAStartedPanelLivesInAProcessGroupOfItsOwn(t *testing.T) {
	p, _ := startHelper(t, "listen")
	pgid, err := syscall.Getpgid(p.PID)
	if err != nil {
		t.Fatal(err)
	}
	own, _ := syscall.Getpgid(0)
	if pgid == own {
		t.Fatal("the panel shares the starter's process group: it would get the window's signals")
	}
	if pgid != p.PID {
		t.Fatalf("process group %d, want the panel to lead its own (%d)", pgid, p.PID)
	}
}

// The panel is the starter's direct child. A panel told to go when its window
// goes watches its parent (cmd/fleetdeck, owner.go); a start wrapped in a shell
// -- for its environment, say -- would make the shell the parent, and the
// panel would outlive the window with nothing else noticing. This is what
// notices.
func TestAStartedPanelIsTheStartersOwnChild(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "panel.log")
	startHelperLogging(t, "listen", logPath)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("helper ppid: %d\n", os.Getpid()); !strings.Contains(string(data), want) {
		t.Fatalf("the panel's parent is not the process that started it; its log holds:\n%s", data)
	}
}

func TestStoppingAPanelFreesItsPort(t *testing.T) {
	p, addr := startHelper(t, "listen")
	if err := p.Stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		t.Fatal("the port still answers after Stop")
	}
	if err := syscall.Kill(p.PID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("the process is still there after Stop: %v", err)
	}
}

// Stop asks first. SIGKILL gives the panel no chance to say goodbye to the
// daemon or finish a write; a Stop that only ever waited and then killed
// would still free the port, and every other test here would pass -- found
// by mutation. The sign is the stand-in's own words in its log: nothing but
// SIGTERM reaching it puts them there.
func TestStopAsksThePanelToLeaveBeforeKillingIt(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "panel.log")
	p, _ := startHelperLogging(t, "listen", logPath)
	if err := p.Stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), termNotice) {
		t.Fatalf("the panel never got SIGTERM; its log holds:\n%s", data)
	}
}

// A panel that does not go on SIGTERM is not left holding the port: the new
// one could never start.
func TestAPanelThatIgnoresTermIsKilled(t *testing.T) {
	p, addr := startHelper(t, "ignore-term")

	// Control: the stand-in really does survive SIGTERM. Without this the
	// case would pass just as well if Stop never escalated at all.
	_ = syscall.Kill(p.PID, syscall.SIGTERM)
	time.Sleep(300 * time.Millisecond)
	if err := syscall.Kill(p.PID, 0); err != nil {
		t.Fatalf("the stand-in died on SIGTERM (%v): this case cannot see the escalation", err)
	}

	if err := p.Stop(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		t.Fatal("the port still answers: the stubborn panel was not killed")
	}
}

func TestWaitingForANPanelThatNeverAnswersEnds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := WaitAnswer(ctx, "http://"+freeAddr(t)+"/")
	if err == nil {
		t.Fatal("WaitAnswer said a port nobody listens on answered")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("WaitAnswer ignored its deadline")
	}
}

// --- one update at a time -----------------------------------------------------

func TestASecondUpdateWhileOneRunsIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	release, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Acquire: %v, want ErrBusy", err)
	}
	release()
	again, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	again()
}

// The lock lives outside the tree. A file inside it would mark every build
// from the tree as built from a modified tree.
func TestTheLockIsNotInsideTheTree(t *testing.T) {
	tree := t.TempDir()
	path, err := LockPath(tree)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, tree+string(os.PathSeparator)) {
		t.Fatalf("lock %s is inside the tree %s", path, tree)
	}
	other, _ := LockPath(t.TempDir())
	if other == path {
		t.Fatal("two trees share one lock")
	}
}
