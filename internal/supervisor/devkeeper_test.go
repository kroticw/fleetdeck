package supervisor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A dev app runs beside the installed one, on a port of its own, and must
// never stop a process that is not its own panel: not the installed panel, not
// the panel of a dev app built in another worktree under the same identifier
// and port. Its keeper is told the one binary it may stop (StopsOnly), and
// checks the binary of whatever listens on the port -- the kernel's word, not
// the panel's -- before any signal.

// copyOfTestBinary is this test binary under another path: a stand-in that
// answers like every other, run from a binary that is not the keeper's own.
func copyOfTestBinary(t *testing.T) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "fleetdeck")
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, self, 0o755); err != nil {
		t.Fatal(err)
	}
	return other
}

// devKeeper is a keeper as a dev app runs it: it knows its window, and stops
// only processes of its own panel binary.
func devKeeper(t *testing.T, addr string) *Keeper {
	k := windowKeeper(t, addr)
	k.StopsOnly = os.Args[0]
	return k
}

// refusalFor is what a dev keeper says of a port held by another binary.
func refusalFor(t *testing.T, addr, exe string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("port %s is held by %s, not by this dev app", port, resolved)
}

func TestADevKeeperLeavesAnOrphanOfAnotherBinaryAlone(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	other := copyOfTestBinary(t)
	orphan := foreignPanel(t, addr, other, deadPID(t))
	r := run(t, devKeeper(t, addr))

	e := r.expect(t, Answering, 5*time.Second)
	if want := refusalFor(t, addr, other); e.Detail != want {
		t.Fatalf("Answering detail = %q, want %q", e.Detail, want)
	}
	r.quiet(t, time.Second)
	if !alive(orphan.Process.Pid) || !answersNow("http://"+addr+"/") {
		t.Fatalf("the orphan of another binary (pid %d) was stopped", orphan.Process.Pid)
	}
}

func TestADevKeeperReplacesAnOrphanOfItsOwnBinary(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	orphan := foreignPanel(t, addr, os.Args[0], deadPID(t))
	r := run(t, devKeeper(t, addr))

	r.expect(t, Replacing, 5*time.Second)
	r.expect(t, Starting, 15*time.Second)
	if up := r.expect(t, Answering, 10*time.Second); !up.Ours {
		t.Fatalf("Answering %+v, want the keeper's own panel", up)
	}
	if alive(orphan.Process.Pid) {
		t.Fatalf("the orphan of the dev app's own binary (pid %d) is still running", orphan.Process.Pid)
	}
}

func TestADevKeeperRefusesAPressToReplaceAPanelOfAnotherBinary(t *testing.T) {
	needLsof(t)
	addr := freeAddr(t)
	other := copyOfTestBinary(t)
	terminal := foreignPanel(t, addr, other, 0)
	k := devKeeper(t, addr)
	r := run(t, k)

	shown := r.expect(t, Answering, 5*time.Second)
	if shown.Holder == nil || shown.Holder.PID != terminal.Process.Pid {
		t.Fatalf("Answering %+v, want the terminal panel (pid %d) as the holder", shown, terminal.Process.Pid)
	}
	k.Replace(*shown.Holder)

	e := r.expect(t, Answering, 5*time.Second)
	if want := refusalFor(t, addr, other); e.Detail != want {
		t.Fatalf("after the press, Answering detail = %q, want %q", e.Detail, want)
	}
	r.quiet(t, time.Second)
	if !alive(terminal.Process.Pid) || !answersNow("http://"+addr+"/") {
		t.Fatalf("the panel of another binary (pid %d) was stopped at a press", terminal.Process.Pid)
	}
}

// The panel's port is in its arguments, and an update's restart from the
// canonical path starts it with the same arguments: a panel restarted without
// --port would take server.port, the installed app's port.
func TestARestartedPanelGetsTheSameArguments(t *testing.T) {
	addr := freeAddr(t)
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	k := newKeeper(t, "listen", addr)
	k.Args = []string{"--port", port}
	k.MinUptime = time.Minute
	r := run(t, k)
	r.expect(t, Starting, 5*time.Second)
	r.expect(t, Answering, 10*time.Second)

	k.Restart(os.Args[0])
	r.expect(t, Starting, 10*time.Second)
	r.expect(t, Answering, 10*time.Second)

	data, _ := os.ReadFile(k.LogPath)
	line := fmt.Sprintf("helper args: %q", k.Args)
	if n := strings.Count(string(data), line); n != 2 {
		t.Fatalf("%q is in the log %d times, want once for each start; the log holds:\n%s", line, n, data)
	}
}
