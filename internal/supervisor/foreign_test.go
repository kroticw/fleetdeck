package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A panel the keeper did not start is used as it is, and the window shows it.
// On 2026-09-14 the operator's v0.9.0 window showed a panel built from source
// three days earlier, kept on the port by a launch agent, and nothing on
// screen said the two were different builds. So the keeper now reports what
// such a panel says of its own build, for the window to compare with its own;
// and it replaces one when -- and only when -- a person asks.

// builtStandIn is a copy of the stand-in binary with a revision file beside
// it, which the stand-in reports as its build's revision.
func builtStandIn(t *testing.T, revision string) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "fleetdeck")
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, self, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "revision"), []byte(revision+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return exe
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestAPanelTheKeeperDidNotStartIsReportedWithWhatItSaysOfItsBuild(t *testing.T) {
	addr := freeAddr(t)
	exe := builtStandIn(t, "de3aa16c0ffee")
	foreignPanel(t, addr, exe, 0)
	r := run(t, windowKeeper(t, addr))

	up := r.expect(t, Answering, 5*time.Second)
	if up.Ours {
		t.Fatalf("Answering %+v, want the panel already there", up)
	}
	if up.Holder == nil {
		t.Fatal("Answering carries no build for a fleetdeck panel the keeper did not start")
	}
	if up.Holder.Revision != "de3aa16c0ffee" {
		t.Errorf("Holder.Revision = %q, want the revision the panel reports", up.Holder.Revision)
	}
	if !sameFile(t, up.Holder.Executable, exe) {
		t.Errorf("Holder.Executable = %q, want %q", up.Holder.Executable, exe)
	}
}

func TestAKeeperWithNoWindowReportsTheBuildToo(t *testing.T) {
	addr := freeAddr(t)
	foreignPanel(t, addr, builtStandIn(t, "abc"), 0)
	r := run(t, newKeeper(t, "listen", addr))

	if up := r.expect(t, Answering, 5*time.Second); up.Holder == nil || up.Holder.Revision != "abc" {
		t.Fatalf("Answering %+v, want the panel's build", up)
	}
}

func TestSomethingThatIsNotAPanelIsReportedWithNoBuild(t *testing.T) {
	addr := freeAddr(t)
	serveElsewhere(t, addr)
	r := run(t, windowKeeper(t, addr))

	if up := r.expect(t, Answering, 5*time.Second); up.Ours || up.Holder != nil {
		t.Fatalf("Answering %+v, want no build: what answers is not a fleetdeck panel", up)
	}
}

func TestReplaceStopsAPanelFromATerminalAndStartsTheKeepersOwn(t *testing.T) {
	needLsof(t) // replacing stops the holder, found by lsof
	addr := freeAddr(t)
	terminal := foreignPanel(t, addr, os.Args[0], 0)
	k := windowKeeper(t, addr)
	r := run(t, k)

	if up := r.expect(t, Answering, 5*time.Second); up.Ours {
		t.Fatalf("Answering %+v, want the terminal's panel", up)
	}
	k.Replace()
	replacing := r.expect(t, Replacing, 5*time.Second)
	if !replacing.Asked {
		t.Errorf("Replacing %+v, want it marked as asked for", replacing)
	}
	r.expect(t, Starting, 10*time.Second)
	if up := r.expect(t, Answering, 10*time.Second); !up.Ours {
		t.Fatalf("Answering %+v, want the keeper's own panel", up)
	}
	if alive(terminal.Process.Pid) {
		t.Fatal("the terminal's panel is still running after it was replaced")
	}
}

// A press that came before the panel it was about -- a page of a panel
// already gone, a second press -- must not stop whatever answers next.
func TestAReplaceAskedBeforeThePanelAnsweredStopsNothing(t *testing.T) {
	addr := freeAddr(t)
	terminal := foreignPanel(t, addr, os.Args[0], 0)
	k := windowKeeper(t, addr)
	k.Replace()
	r := run(t, k)

	if up := r.expect(t, Answering, 5*time.Second); up.Ours {
		t.Fatalf("Answering %+v, want the terminal's panel", up)
	}
	r.quiet(t, time.Second)
	if !alive(terminal.Process.Pid) {
		t.Fatal("a Replace asked before the panel answered stopped it")
	}
}
